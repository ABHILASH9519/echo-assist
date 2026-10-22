package main

import (
 "context"
 "crypto/rand"
 "database/sql"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "log"
 "net/http"
 "os"
 "path/filepath"
 "regexp"
 "sort"
 "strconv"
 "strings"
 "sync"
 "time"
 _ "time/tzdata"
 "os/signal"
 "syscall"

 "github.com/aws/aws-sdk-go-v2/aws"
 "github.com/aws/aws-sdk-go-v2/config"
 "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
 bt "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
 _ "github.com/jackc/pgx/v5/stdlib"
)

type Task struct {
 ID string `json:"id"`
 Title string `json:"title"`
 Details string `json:"details"`
 Completed bool `json:"completed"`
 Priority string `json:"priority"`
 DueDate *string `json:"dueDate"`
 CreatedAt time.Time `json:"createdAt"`
 CompletedAt *time.Time `json:"completedAt,omitempty"`
}
type Store struct { mu sync.RWMutex; tasks []Task; db *sql.DB; file, mode string }
type App struct { store *Store; bedrock *bedrockruntime.Client; aiConfigured bool; region string; startedAt time.Time }
type apiError struct { Error string `json:"error"` }

func main() {
 ctx:=context.Background()
 dataDir:=env("DATA_DIR",".data"); if err:=os.MkdirAll(dataDir,0750);err!=nil{log.Fatal(err)}
 s:=&Store{file:filepath.Join(dataDir,"tasks.json"),mode:"file",tasks:[]Task{}}
 if dsn:=os.Getenv("DATABASE_URL");dsn!="" {
  db,err:=sql.Open("pgx",dsn)
  if err==nil {db.SetMaxOpenConns(5); db.SetMaxIdleConns(2); c,cancel:=context.WithTimeout(ctx,8*time.Second);err=db.PingContext(c);cancel()}
  if err==nil {_,err=db.ExecContext(ctx,`CREATE TABLE IF NOT EXISTS echo_tasks (id TEXT PRIMARY KEY,title TEXT NOT NULL,details TEXT NOT NULL DEFAULT '',completed BOOLEAN NOT NULL DEFAULT FALSE,priority TEXT NOT NULL DEFAULT 'medium',due_date DATE NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),completed_at TIMESTAMPTZ NULL)`)}
  if err==nil {s.db=db;s.mode="postgres";err=s.loadDB(ctx)}
  if err!=nil {if db!=nil{_ = db.Close()};s.db=nil;s.mode="file";log.Printf("PostgreSQL unavailable; using file storage: %v",err)}
 }
 if s.db==nil{s.loadFile()}
 region:=env("AWS_REGION","ap-south-1")
 cfg,awsErr:=config.LoadDefaultConfig(ctx,config.WithRegion(region))
 app:=&App{store:s,region:region,startedAt:time.Now()}
 if awsErr==nil {
  app.bedrock=bedrockruntime.NewFromConfig(cfg)
  if strings.TrimSpace(os.Getenv("BEDROCK_MODEL_ID"))!="" { credCtx,cancel:=context.WithTimeout(ctx,3*time.Second);_,credErr:=cfg.Credentials.Retrieve(credCtx);cancel();app.aiConfigured=credErr==nil;if credErr!=nil{log.Printf("Bedrock credentials unavailable: %v",credErr)} }
 } else { log.Printf("AWS configuration unavailable: %v",awsErr) }
 mux:=http.NewServeMux()
 mux.HandleFunc("/api/health",app.health);mux.HandleFunc("/api/tasks",app.tasks);mux.HandleFunc("/api/stats",app.stats);mux.HandleFunc("/api/agenda",app.agenda);mux.HandleFunc("/api/assistant",app.assistant);mux.HandleFunc("/api/tasks/",app.taskByID);mux.HandleFunc("/mcp",app.mcp)
 mux.Handle("/",http.FileServer(http.Dir("public")))
 port:=env("PORT","3000")
 srv:=&http.Server{Addr:"0.0.0.0:"+port,Handler:securityHeaders(mux),ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:35*time.Second,IdleTimeout:60*time.Second}
 go func(){log.Printf("Echo Assist Go listening on :%s; storage=%s; bedrockConfigured=%t",port,s.mode,app.aiConfigured);if err:=srv.ListenAndServe();err!=nil&&!errors.Is(err,http.ErrServerClosed){log.Fatalf("HTTP server failed: %v",err)}}()
 stop:=make(chan os.Signal,1);signal.Notify(stop,syscall.SIGINT,syscall.SIGTERM);<-stop
 shutdownCtx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();if err:=srv.Shutdown(shutdownCtx);err!=nil{log.Printf("Graceful shutdown failed: %v",err)}
 if s.db!=nil{_ = s.db.Close()}

}
func env(k,d string)string{if v:=strings.TrimSpace(os.Getenv(k));v!=""{return v};return d}
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","strict-origin-when-cross-origin");w.Header().Set("X-Frame-Options","DENY");w.Header().Set("Permissions-Policy","camera=(), microphone=(), geolocation=()");w.Header().Set("Content-Security-Policy","default-src 'self'; script-src 'self' 'unsafe-inline' https://unpkg.com; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com data:; img-src 'self' data: https:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'");next.ServeHTTP(w,r)})}
func jsonOut(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json; charset=utf-8");w.WriteHeader(status);_ = json.NewEncoder(w).Encode(v)}
func fail(w http.ResponseWriter,status int,msg string){jsonOut(w,status,apiError{msg})}
func decode(w http.ResponseWriter,r *http.Request,v any)error{
 defer r.Body.Close()
 d:=json.NewDecoder(http.MaxBytesReader(w,r.Body,32<<10))
 if err:=d.Decode(v);err!=nil{return err}
 var extra any
 if err:=d.Decode(&extra);err!=io.EOF{return errors.New("request body must contain a single JSON value")}
 return nil
}
func (s *Store)loadFile(){b,e:=os.ReadFile(s.file);if e==nil{if json.Unmarshal(b,&s.tasks)!=nil{s.tasks=[]Task{}}}else{s.tasks=[]Task{}}}
func (s *Store)loadDB(ctx context.Context)error{
 rows,e:=s.db.QueryContext(ctx,`SELECT id,title,details,completed,priority,due_date::text,created_at,completed_at FROM echo_tasks ORDER BY created_at DESC`);if e!=nil{return e};defer rows.Close();out:=[]Task{}
 for rows.Next(){var t Task;var due sql.NullString;var completed sql.NullTime;if e:=rows.Scan(&t.ID,&t.Title,&t.Details,&t.Completed,&t.Priority,&due,&t.CreatedAt,&completed);e!=nil{return e};if due.Valid{v:=due.String;t.DueDate=&v};if completed.Valid{v:=completed.Time;t.CompletedAt=&v};out=append(out,t)}
 if e:=rows.Err();e!=nil{return e};s.tasks=out;return nil
}
func (s *Store)snapshot()[]Task{s.mu.RLock();defer s.mu.RUnlock();out:=make([]Task,len(s.tasks));copy(out,s.tasks);return out}
func (s *Store)persist(ctx context.Context)error{
 if s.db!=nil{tx,e:=s.db.BeginTx(ctx,nil);if e!=nil{return e};defer tx.Rollback();if _,e=tx.ExecContext(ctx,"DELETE FROM echo_tasks");e!=nil{return e};for _,t:=range s.tasks{var due,completed any;if t.DueDate!=nil{due=*t.DueDate};if t.CompletedAt!=nil{completed=*t.CompletedAt};if _,e=tx.ExecContext(ctx,`INSERT INTO echo_tasks(id,title,details,completed,priority,due_date,created_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,t.ID,t.Title,t.Details,t.Completed,t.Priority,due,t.CreatedAt,completed);e!=nil{return e}};return tx.Commit()}
 if e:=os.MkdirAll(filepath.Dir(s.file),0750);e!=nil{return e};b,e:=json.MarshalIndent(s.tasks,"","  ");if e!=nil{return e};tmp:=s.file+".tmp";if e=os.WriteFile(tmp,b,0600);e!=nil{return e};return os.Rename(tmp,s.file)
}
func clean(v string,n int)string{v=strings.TrimSpace(v);if len(v)>n{v=v[:n]};return v}
func priority(v string)string{v=strings.ToLower(strings.TrimSpace(v));if v=="low"||v=="medium"||v=="high"{return v};return "medium"}
func normalizePriority(v string)(string,error){v=strings.ToLower(strings.TrimSpace(v));if v==""{return "medium",nil};if v=="low"||v=="medium"||v=="high"{return v,nil};return "",errors.New("Priority must be low, medium, or high.")}
func appLocation()*time.Location{loc,err:=time.LoadLocation(env("APP_TIMEZONE","Asia/Kolkata"));if err!=nil{return time.FixedZone("UTC+05:30",5*3600+1800)};return loc}
func appToday()string{return time.Now().In(appLocation()).Format("2006-01-02")}
var dateRE=regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
func dueDate(v *string)(*string,error){if v==nil||strings.TrimSpace(*v)==""{return nil,nil};x:=strings.TrimSpace(*v);d,e:=time.Parse("2006-01-02",x);if !dateRE.MatchString(x)||e!=nil||d.Format("2006-01-02")!=x{return nil,errors.New("Choose a valid due date.")};return &x,nil}
func newID()string{b:=make([]byte,16);if _,e:=rand.Read(b);e!=nil{return fmt.Sprintf("%d",time.Now().UnixNano())};b[6]=(b[6]&15)|64;b[8]=(b[8]&63)|128;return fmt.Sprintf("%x-%x-%x-%x-%x",b[:4],b[4:6],b[6:8],b[8:10],b[10:])}
func (s *Store)create(title,details,prio string,due *string)(Task,error){
 title=clean(title,180);if title==""{return Task{},errors.New("Please enter a task title.")};p,e:=normalizePriority(prio);if e!=nil{return Task{},e};d,e:=dueDate(due);if e!=nil{return Task{},e};t:=Task{ID:newID(),Title:title,Details:clean(details,500),Priority:p,DueDate:d,CreatedAt:time.Now().UTC()}
 s.mu.Lock();defer s.mu.Unlock();s.tasks=append([]Task{t},s.tasks...);ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();if e=s.persist(ctx);e!=nil{s.tasks=s.tasks[1:];return Task{},e};return t,nil
}
func (s *Store)complete(id string)(Task,error){s.mu.Lock();defer s.mu.Unlock();for i:=range s.tasks{if s.tasks[i].ID==id{old:=s.tasks[i];now:=time.Now().UTC();s.tasks[i].Completed=true;s.tasks[i].CompletedAt=&now;ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();if e:=s.persist(ctx);e!=nil{s.tasks[i]=old;return Task{},e};return s.tasks[i],nil}};return Task{},errors.New("Task not found.")}
func (a *App)health(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{w.Header().Set("Allow","GET");fail(w,405,"Method not allowed.");return};dbHealthy:=false;if a.store.db!=nil{ctx,cancel:=context.WithTimeout(r.Context(),2*time.Second);dbHealthy=a.store.db.PingContext(ctx)==nil;cancel()};jsonOut(w,200,map[string]any{"ok":true,"app":"Echo Assist","language":"Go","aiConfigured":a.aiConfigured,"storage":a.store.mode,"databaseHealthy":dbHealthy,"taskCount":len(a.store.snapshot()),"timezone":env("APP_TIMEZONE","Asia/Kolkata"),"uptimeSeconds":int64(time.Since(a.startedAt).Seconds())})}
func (a *App)tasks(w http.ResponseWriter,r *http.Request){switch r.Method{case "GET":
 all:=a.store.snapshot();q:=strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")));status:=strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")));prio:=strings.ToLower(strings.TrimSpace(r.URL.Query().Get("priority")));due:=strings.TrimSpace(r.URL.Query().Get("dueDate"));if status!=""&&status!="all"&&status!="open"&&status!="completed"{fail(w,400,"status must be open, completed, or all.");return};if prio!=""&&prio!="low"&&prio!="medium"&&prio!="high"{fail(w,400,"priority must be low, medium, or high.");return};if due!=""{d,e:=dueDate(&due);if e!=nil||d==nil{fail(w,400,"dueDate must use YYYY-MM-DD.");return}};filtered:=make([]Task,0,len(all));for _,t:=range all{if q!=""&&!strings.Contains(strings.ToLower(t.Title+" "+t.Details),q){continue};if status=="open"&&t.Completed||status=="completed"&&!t.Completed{continue};if prio!=""&&t.Priority!=prio{continue};if due!=""&&(t.DueDate==nil||*t.DueDate!=due){continue};filtered=append(filtered,t)};limit:=100;if v:=r.URL.Query().Get("limit");v!=""{n,e:=strconv.Atoi(v);if e!=nil||n<1||n>200{fail(w,400,"limit must be between 1 and 200.");return};limit=n};offset:=0;if v:=r.URL.Query().Get("offset");v!=""{n,e:=strconv.Atoi(v);if e!=nil||n<0{fail(w,400,"offset must be zero or greater.");return};offset=n};total:=len(filtered);if offset>total{offset=total};end:=offset+limit;if end>total{end=total};jsonOut(w,200,map[string]any{"tasks":filtered[offset:end],"total":total,"limit":limit,"offset":offset})
 case "POST":var b struct{Title string `json:"title"`;Details string `json:"details"`;Priority string `json:"priority"`;DueDate *string `json:"dueDate"`};if decode(w,r,&b)!=nil{fail(w,400,"Invalid JSON request.");return};t,e:=a.store.create(b.Title,b.Details,b.Priority,b.DueDate);if e!=nil{fail(w,400,e.Error());return};jsonOut(w,201,map[string]any{"task":t});default:w.Header().Set("Allow","GET, POST");fail(w,405,"Method not allowed.")}}
func (a *App)taskByID(w http.ResponseWriter,r *http.Request){
 rest:=strings.Trim(strings.TrimPrefix(r.URL.Path,"/api/tasks/"),"/");p:=strings.Split(rest,"/");if rest==""{http.NotFound(w,r);return};id:=p[0]
 if len(p)==2&&p[1]=="complete"&&r.Method=="PATCH"{t,e:=a.store.complete(id);if e!=nil{fail(w,404,e.Error());return};jsonOut(w,200,map[string]any{"task":t});return}
 if len(p)!=1{http.NotFound(w,r);return}
 s:=a.store;s.mu.Lock();defer s.mu.Unlock();idx:=-1;for i,t:=range s.tasks{if t.ID==id{idx=i;break}};if idx<0{fail(w,404,"Task not found.");return}
 old:=s.tasks[idx]
 if r.Method=="DELETE"{s.tasks=append(s.tasks[:idx],s.tasks[idx+1:]...);ctx,c:=context.WithTimeout(r.Context(),10*time.Second);defer c();if e:=s.persist(ctx);e!=nil{s.tasks=append(s.tasks,nil...);s.tasks=append(s.tasks[:idx],append([]Task{old},s.tasks[idx:]...)...);fail(w,500,"Could not save task changes.");return};w.WriteHeader(204);return}
 if r.Method=="PATCH"{var b map[string]json.RawMessage;if decode(w,r,&b)!=nil{fail(w,400,"Invalid JSON request.");return}
  str:=func(k string)(string,bool){raw,ok:=b[k];if !ok{return "",false};var v string;if json.Unmarshal(raw,&v)!=nil{return "",false};return v,true}
  if _,ok:=b["title"];ok{v,good:=str("title");if !good||clean(v,180)==""{fail(w,400,"Please enter a valid task title.");return};s.tasks[idx].Title=clean(v,180)}
  if _,ok:=b["details"];ok{v,good:=str("details");if !good{fail(w,400,"Invalid details.");return};s.tasks[idx].Details=clean(v,500)}
  if _,ok:=b["priority"];ok{v,good:=str("priority");if !good{fail(w,400,"Invalid priority.");return};p,e:=normalizePriority(v);if e!=nil{fail(w,400,e.Error());return};s.tasks[idx].Priority=p}
  if raw,ok:=b["dueDate"];ok{var v *string;if string(raw)!="null"{var x string;if json.Unmarshal(raw,&x)!=nil{fail(w,400,"Invalid due date.");return};v=&x};d,e:=dueDate(v);if e!=nil{fail(w,400,e.Error());return};s.tasks[idx].DueDate=d}
  ctx,c:=context.WithTimeout(r.Context(),10*time.Second);defer c();if e:=s.persist(ctx);e!=nil{s.tasks[idx]=old;fail(w,500,"Could not save task changes.");return};jsonOut(w,200,map[string]any{"task":s.tasks[idx]});return
 }
 w.Header().Set("Allow","PATCH, DELETE");fail(w,405,"Method not allowed.")
}
 func (a *App)stats(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{w.Header().Set("Allow","GET");fail(w,405,"Method not allowed.");return};items:=a.store.snapshot();out:=map[string]any{"total":len(items),"open":0,"completed":0,"overdue":0,"dueToday":0,"byPriority":map[string]int{"high":0,"medium":0,"low":0},"date":appToday(),"timezone":env("APP_TIMEZONE","Asia/Kolkata")};for _,t:=range items{if t.Completed{out["completed"]=out["completed"].(int)+1;continue};out["open"]=out["open"].(int)+1;by:=out["byPriority"].(map[string]int);by[t.Priority]++;if t.DueDate!=nil{if *t.DueDate==appToday(){out["dueToday"]=out["dueToday"].(int)+1}else if *t.DueDate<appToday(){out["overdue"]=out["overdue"].(int)+1}}};jsonOut(w,200,out)}
func (a *App)agenda(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{w.Header().Set("Allow","GET");fail(w,405,"Method not allowed.");return};date:=r.URL.Query().Get("date");if date==""{date=appToday()};d,e:=dueDate(&date);if e!=nil||d==nil{fail(w,400,"Use a date in YYYY-MM-DD format.");return};due,over:=[]Task{},[]Task{};rank:=map[string]int{"high":0,"medium":1,"low":2};for _,t:=range a.store.snapshot(){if t.Completed||t.DueDate==nil{continue};if *t.DueDate==date{due=append(due,t)}else if *t.DueDate<date{over=append(over,t)}};sort.SliceStable(due,func(i,j int)bool{return rank[due[i].Priority]<rank[due[j].Priority]});sort.SliceStable(over,func(i,j int)bool{return rank[over[i].Priority]<rank[over[j].Priority]});jsonOut(w,200,map[string]any{"date":date,"tasks":due,"overdue":over})}
func (a *App)assistant(w http.ResponseWriter,r *http.Request){
 if r.Method!="POST"{fail(w,405,"Method not allowed.");return};var b struct{Message string `json:"message"`};if decode(w,r,&b)!=nil{fail(w,400,"Invalid JSON request.");return};msg:=clean(b.Message,1000);if msg==""{fail(w,400,"Enter a message first.");return};lower:=strings.ToLower(msg)
 if m := regexp.MustCompile(`(?i)^(?:add|create|remember|remind me to)\s+(.+)$`).FindStringSubmatch(msg); len(m) == 2 {
  title := strings.TrimSpace(m[1])
  var due *string
  dueRE := regexp.MustCompile(`(?i)\s+(?:due|by)\s+(\d{4}-\d{2}-\d{2})`)
  if x := dueRE.FindStringSubmatch(title); len(x) == 2 {
   value := x[1]
   due = &value
   title = dueRE.ReplaceAllString(title, "")
  }
  prio := "medium"
  priorityRE := regexp.MustCompile(`(?i)\s+priority\s+(low|medium|high)\s*$`)
  if x := priorityRE.FindStringSubmatch(title); len(x) == 2 {
   prio = strings.ToLower(x[1])
   title = priorityRE.ReplaceAllString(title, "")
  }
  t, err := a.store.create(title, "", prio, due)
  if err != nil { fail(w, 400, err.Error()); return }
  reply := fmt.Sprintf("Added “%s” with %s priority", t.Title, t.Priority)
  if t.DueDate != nil { reply += " and a due date of " + *t.DueDate }
  jsonOut(w, 200, map[string]any{"reply": reply + ".", "action": "task_created", "task": t, "mode": "local"})
  return
 }
 if (strings.Contains(lower,"task")||strings.Contains(lower,"todo")||strings.Contains(lower,"to-do"))&&(strings.HasPrefix(lower,"list")||strings.HasPrefix(lower,"show")||strings.HasPrefix(lower,"what are")||strings.HasPrefix(lower,"view")){items:=[]Task{};lines:=[]string{};for _,t:=range a.store.snapshot(){if !t.Completed{items=append(items,t);line:=fmt.Sprintf("%d. [%s] %s",len(items),t.Priority,t.Title);if t.DueDate!=nil{line=fmt.Sprintf("%d. [%s] (due %s) %s",len(items),t.Priority,*t.DueDate,t.Title)};lines=append(lines,line)}};reply:=strings.Join(lines,"\n");if len(items)==0{reply="You have no open tasks. Enjoy the breathing room!"};jsonOut(w,200,map[string]any{"reply":reply,"action":"task_listed","tasks":items,"mode":"local"});return}
 if strings.Contains(lower,"agenda")||strings.Contains(lower,"due today")||strings.Contains(lower,"today's tasks")||strings.Contains(lower,"todays tasks"){today:=appToday();items:=[]Task{};for _,t:=range a.store.snapshot(){if !t.Completed&&t.DueDate!=nil&&*t.DueDate==today{items=append(items,t)}};reply:="No open tasks are due today ("+today+").";if len(items)>0{lines:=[]string{};for i,t:=range items{lines=append(lines,fmt.Sprintf("%d. [%s] %s",i+1,t.Priority,t.Title))};reply="Due today ( "+today+" ):\n"+strings.Join(lines,"\n")};jsonOut(w,200,map[string]any{"reply":reply,"tasks":items,"action":"agenda_listed","mode":"local"});return}
 if m:=regexp.MustCompile(`(?i)^(?:complete|finish|done|mark done)\s+(.+)$`).FindStringSubmatch(msg);len(m)==2{wanted:=strings.ToLower(strings.TrimSpace(m[1]));var match *Task;for _,t:=range a.store.snapshot(){if !t.Completed&&(strings.ToLower(t.Title)==wanted||strings.Contains(strings.ToLower(t.Title),wanted)){v:=t;match=&v;break}};if match==nil{jsonOut(w,200,map[string]any{"reply":"I could not find an open task matching that. Try “list my tasks” to see what is open.","mode":"local"});return};t,e:=a.store.complete(match.ID);if e!=nil{fail(w,500,"Could not complete that task.");return};jsonOut(w,200,map[string]any{"reply":"Completed “"+t.Title+"”. Nice work!","action":"task_completed","task":t,"mode":"local"});return}
 if model:=os.Getenv("BEDROCK_MODEL_ID");model!=""&&a.bedrock!=nil&&a.aiConfigured{ctx,c:=context.WithTimeout(r.Context(),25*time.Second);defer c();out,e:=a.bedrock.Converse(ctx,&bedrockruntime.ConverseInput{ModelId:aws.String(model),System:[]bt.SystemContentBlock{&bt.SystemContentBlockMemberText{Value:"You are Echo Assist, a concise and encouraging productivity assistant. Help the user plan tasks. Never claim to have changed tasks unless the application confirms it."}},Messages:[]bt.Message{{Role:bt.ConversationRoleUser,Content:[]bt.ContentBlock{&bt.ContentBlockMemberText{Value:msg}}}},InferenceConfig:&bt.InferenceConfiguration{MaxTokens:aws.Int32(350),Temperature:aws.Float32(.4)}});if e!=nil{log.Printf("Bedrock failed: %v",e);fail(w,502,"The assistant could not complete that request. Check server configuration and try again.");return};reply:="I could not generate a response just now.";if out.Output!=nil{if m,ok:=out.Output.(*bt.ConverseOutputMemberMessage);ok{var parts []string;for _,v:=range m.Value.Content{if t,ok:=v.(*bt.ContentBlockMemberText);ok{parts=append(parts,t.Value)}};if strings.TrimSpace(strings.Join(parts,""))!=""{reply=strings.TrimSpace(strings.Join(parts,""))}}};jsonOut(w,200,map[string]any{"reply":reply,"mode":"bedrock"});return}
 jsonOut(w,200,map[string]any{"reply":"I can manage tasks locally with “add prepare demo”, “list my tasks”, “complete prepare demo”, or “agenda today”. Add “priority high” or “due 2026-10-30” when creating a task. Open-ended AI responses are not enabled yet: configure BEDROCK_MODEL_ID and AWS credentials to connect Amazon Bedrock.","mode":"local"})
}
func (a *App)mcp(w http.ResponseWriter,r *http.Request){
 if r.Method!="POST"{w.Header().Set("Allow","POST");fail(w,405,"Use POST for stateless MCP requests.");return}
 var q struct{JSONRPC string `json:"jsonrpc"`;ID json.RawMessage `json:"id"`;Method string `json:"method"`;Params struct{Name string `json:"name"`;Arguments map[string]json.RawMessage `json:"arguments"`} `json:"params"`}
 if decode(w,r,&q)!=nil{fail(w,400,"Invalid JSON-RPC request.");return};if len(q.ID)==0{w.WriteHeader(202);return};var result any=map[string]any{}
 switch q.Method{
 case "initialize":result=map[string]any{"protocolVersion":"2025-03-26","capabilities":map[string]any{"tools":map[string]any{}},"serverInfo":map[string]string{"name":"echo-assist","version":"2.0.0"}}
 case "ping":result=map[string]any{}
 case "tools/list":result=map[string]any{"tools":[]any{
 map[string]any{"name":"add_task","description":"Create a task in Echo Assist","inputSchema":map[string]any{"type":"object","properties":map[string]any{"title":map[string]any{"type":"string","minLength":1,"maxLength":180},"details":map[string]any{"type":"string","maxLength":500},"priority":map[string]any{"type":"string","enum":[]string{"low","medium","high"}},"due_date":map[string]any{"type":"string","pattern":`^\d{4}-\d{2}-\d{2}$`}},"required":[]string{"title"}}},
 map[string]any{"name":"list_tasks","description":"List tasks in Echo Assist","inputSchema":map[string]any{"type":"object","properties":map[string]any{"include_completed":map[string]any{"type":"boolean"}}}},
 map[string]any{"name":"complete_task","description":"Mark a task complete by its exact task ID","inputSchema":map[string]any{"type":"object","properties":map[string]any{"task_id":map[string]any{"type":"string","minLength":1}},"required":[]string{"task_id"}}},
 }}
 case "tools/call":
 args:=q.Params.Arguments
 switch q.Params.Name{
 case "add_task":var title,details,prio string;var due *string;if json.Unmarshal(args["title"],&title)!=nil{result=mcpError("title is required");break};_ = json.Unmarshal(args["details"],&details);_ = json.Unmarshal(args["priority"],&prio);if raw:=args["due_date"];len(raw)>0{var v string;if json.Unmarshal(raw,&v)==nil{due=&v}};t,e:=a.store.create(title,details,prio,due);if e!=nil{result=mcpError(e.Error())}else{result=mcpResult(t)}
 case "list_tasks":var all bool;_ = json.Unmarshal(args["include_completed"],&all);items:=a.store.snapshot();if !all{filtered:=[]Task{};for _,t:=range items{if !t.Completed{filtered=append(filtered,t)}};items=filtered};result=mcpResult(items)
 case "complete_task":var id string;if json.Unmarshal(args["task_id"],&id)!=nil||id==""{result=mcpError("task_id is required");break};t,e:=a.store.complete(id);if e!=nil{result=mcpError(e.Error())}else{result=mcpResult(t)}
 default:result=mcpError("Unknown tool: "+q.Params.Name)
 }
 default:jsonOut(w,200,map[string]any{"jsonrpc":"2.0","id":q.ID,"error":map[string]any{"code":-32601,"message":"Method not found"}});return
 }
 jsonOut(w,200,map[string]any{"jsonrpc":"2.0","id":q.ID,"result":result})
}
func mcpResult(v any)map[string]any{b,_:=json.Marshal(v);return map[string]any{"content":[]any{map[string]any{"type":"text","text":string(b)}}}}
func mcpError(v string)map[string]any{return map[string]any{"content":[]any{map[string]any{"type":"text","text":v}},"isError":true}}
