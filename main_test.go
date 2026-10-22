package main

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

func testApp(t *testing.T) *App {
 t.Helper()
 return &App{store:&Store{file:filepath.Join(t.TempDir(),"tasks.json"),mode:"file",tasks:[]Task{}},region:"ap-south-1",startedAt:time.Now()}
}
func requestJSON(t *testing.T,a *App,method,path,body string)*httptest.ResponseRecorder{
 t.Helper();r:=httptest.NewRequest(method,path,strings.NewReader(body));r.Header.Set("Content-Type","application/json");w:=httptest.NewRecorder()
 switch {
 case path=="/api/tasks":a.tasks(w,r)
 case path=="/api/stats":a.stats(w,r)
 case strings.HasPrefix(path,"/api/tasks/"):a.taskByID(w,r)
 case strings.HasPrefix(path,"/api/agenda"):a.agenda(w,r)
 case path=="/api/assistant":a.assistant(w,r)
 case path=="/mcp":a.mcp(w,r)
 default:t.Fatalf("unhandled test path %s",path)
 };return w
}
func TestTaskLifecycleAPI(t *testing.T){
 a:=testApp(t);w:=requestJSON(t,a,http.MethodPost,"/api/tasks",`{"title":"Ship demo","priority":"high","dueDate":"2026-10-30"}`)
 if w.Code!=http.StatusCreated{t.Fatalf("create status %d: %s",w.Code,w.Body.String())}
 var p struct{Task Task `json:"task"`};if e:=json.Unmarshal(w.Body.Bytes(),&p);e!=nil{t.Fatal(e)}
 if p.Task.Title!="Ship demo"||p.Task.Priority!="high"||p.Task.DueDate==nil||*p.Task.DueDate!="2026-10-30"{t.Fatalf("unexpected task: %+v",p.Task)}
 w=requestJSON(t,a,http.MethodPatch,"/api/tasks/"+p.Task.ID+"/complete","")
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),`"completed":true`){t.Fatalf("complete status %d: %s",w.Code,w.Body.String())}
 w=requestJSON(t,a,http.MethodDelete,"/api/tasks/"+p.Task.ID,"")
 if w.Code!=http.StatusNoContent{t.Fatalf("delete status %d: %s",w.Code,w.Body.String())}
 if len(a.store.snapshot())!=0{t.Fatal("expected no tasks after delete")}
}
func TestInvalidDateAndPriorityRejected(t *testing.T){
 a:=testApp(t)
 for _,body:=range []string{`{"title":"bad","dueDate":"2026-02-31"}`,`{"title":"bad","priority":"urgent"}`}{
  w:=requestJSON(t,a,http.MethodPost,"/api/tasks",body);if w.Code!=http.StatusBadRequest{t.Fatalf("expected 400 for %s, got %d",body,w.Code)}
 }
}
func TestTaskFilteringAndPagination(t *testing.T){
 a:=testApp(t)
 for _,body:=range []string{`{"title":"Release backend","priority":"high"}`,`{"title":"Release frontend","priority":"low"}`,`{"title":"Buy groceries","priority":"medium"}`}{
  if w:=requestJSON(t,a,http.MethodPost,"/api/tasks",body);w.Code!=http.StatusCreated{t.Fatalf("create failed: %s",w.Body.String())}
 }
 w:=requestJSON(t,a,http.MethodGet,"/api/tasks?q=release&status=open&limit=1","")
 if w.Code!=http.StatusOK{t.Fatalf("filter status %d",w.Code)}
 var p struct{Tasks []Task `json:"tasks"`;Total int `json:"total"`;Limit int `json:"limit"`}
 if e:=json.Unmarshal(w.Body.Bytes(),&p);e!=nil{t.Fatal(e)}
 if len(p.Tasks)!=1||p.Total!=2||p.Limit!=1{t.Fatalf("unexpected filtered page: %+v",p)}
}
func TestAgendaAndStats(t *testing.T){
 a:=testApp(t);today:=appToday();yesterday:=time.Now().In(appLocation()).AddDate(0,0,-1).Format("2006-01-02")
 if _,e:=a.store.create("Due today","", "high",&today);e!=nil{t.Fatal(e)}
 if _,e:=a.store.create("Overdue","", "medium",&yesterday);e!=nil{t.Fatal(e)}
 w:=requestJSON(t,a,http.MethodGet,"/api/agenda?date="+today,"")
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),`"overdue"`){t.Fatalf("agenda response %d: %s",w.Code,w.Body.String())}
 w=requestJSON(t,a,http.MethodGet,"/api/stats","")
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),`"dueToday":1`)||!strings.Contains(w.Body.String(),`"overdue":1`){t.Fatalf("stats response %d: %s",w.Code,w.Body.String())}
}
func TestAssistantCreatesAndCompletesTask(t *testing.T){
 a:=testApp(t)
 w:=requestJSON(t,a,http.MethodPost,"/api/assistant",`{"message":"add prepare demo priority high due 2026-10-30"}`)
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),`"task_created"`){t.Fatalf("assistant create %d: %s",w.Code,w.Body.String())}
 w=requestJSON(t,a,http.MethodPost,"/api/assistant",`{"message":"complete prepare demo"}`)
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),`"task_completed"`){t.Fatalf("assistant complete %d: %s",w.Code,w.Body.String())}
}
func TestMCPToolDiscoveryAndCreate(t *testing.T){
 a:=testApp(t)
 w:=requestJSON(t,a,http.MethodPost,"/mcp",`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),"add_task"){t.Fatalf("MCP list %d: %s",w.Code,w.Body.String())}
 w=requestJSON(t,a,http.MethodPost,"/mcp",`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add_task","arguments":{"title":"MCP task","priority":"medium"}}}`)
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),"MCP task"){t.Fatalf("MCP create %d: %s",w.Code,w.Body.String())}
}
func TestHealthReportsRuntimeMode(t *testing.T){
 a:=testApp(t);w:=httptest.NewRecorder();a.health(w,httptest.NewRequest(http.MethodGet,"/api/health",nil))
 if w.Code!=http.StatusOK||!strings.Contains(w.Body.String(),`"language":"Go"`){t.Fatalf("health response %d: %s",w.Code,w.Body.String())}
}

func TestAssistantMetadataOrderVariations(t *testing.T) {
 a := testApp(t)
 commands := []struct {
  message, title, priority, due string
 }{
  {message: "add prepare launch priority high due 2026-10-30", title: "prepare launch", priority: "high", due: "2026-10-30"},
  {message: "add submit report due 2026-10-31 priority low", title: "submit report", priority: "low", due: "2026-10-31"},
 }
 for _, cmd := range commands {
  w := requestJSON(t, a, http.MethodPost, "/api/assistant", `{"message":"`+cmd.message+`"}`)
  if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"task_created"`) {
   t.Fatalf("assistant command %q failed: status=%d body=%s", cmd.message, w.Code, w.Body.String())
  }
 }
 tasks := a.store.snapshot()
 if len(tasks) != 2 { t.Fatalf("expected 2 tasks, got %d", len(tasks)) }
 for _, cmd := range commands {
  var found *Task
  for i := range tasks { if tasks[i].Title == cmd.title { found = &tasks[i]; break } }
  if found == nil || found.Priority != cmd.priority || found.DueDate == nil || *found.DueDate != cmd.due {
   t.Fatalf("command %q parsed incorrectly: %+v", cmd.message, found)
  }
 }
}
