package main

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"
)

func testApp(t *testing.T) *App {
 t.Helper()
 return &App{store: &Store{file: filepath.Join(t.TempDir(), "tasks.json"), mode: "file", tasks: []Task{}}}
}

func TestTaskLifecycleAPI(t *testing.T) {
 a := testApp(t)
 req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`{"title":"Ship demo","priority":"high","dueDate":"2026-10-30"}`))
 rec := httptest.NewRecorder()
 a.tasks(rec, req)
 if rec.Code != http.StatusCreated { t.Fatalf("create status %d: %s", rec.Code, rec.Body.String()) }
 var p struct { Task Task `json:"task"` }
 if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil { t.Fatal(err) }
 if p.Task.Title != "Ship demo" || p.Task.Priority != "high" || p.Task.DueDate == nil || *p.Task.DueDate != "2026-10-30" { t.Fatalf("unexpected task: %+v", p.Task) }
 rec = httptest.NewRecorder()
 a.taskByID(rec, httptest.NewRequest(http.MethodPatch, "/api/tasks/"+p.Task.ID+"/complete", nil))
 if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"completed":true`) { t.Fatalf("complete status %d: %s", rec.Code, rec.Body.String()) }
}

func TestInvalidDateRejected(t *testing.T) {
 a := testApp(t)
 rec := httptest.NewRecorder()
 a.tasks(rec, httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`{"title":"bad","dueDate":"2026-02-31"}`)))
 if rec.Code != http.StatusBadRequest { t.Fatalf("expected 400, got %d", rec.Code) }
}

func TestTaskUpdateAndDelete(t *testing.T) {
 a := testApp(t)
 rec := httptest.NewRecorder()
 a.tasks(rec, httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`{"title":"Original","priority":"low"}`)))
 if rec.Code != http.StatusCreated { t.Fatalf("create status %d: %s", rec.Code, rec.Body.String()) }
 var created struct { Task Task `json:"task"` }
 if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil { t.Fatal(err) }

 rec = httptest.NewRecorder()
 a.taskByID(rec, httptest.NewRequest(http.MethodPatch, "/api/tasks/"+created.Task.ID, strings.NewReader(`{"title":"Updated","priority":"high","dueDate":"2026-10-30"}`)))
 if rec.Code != http.StatusOK { t.Fatalf("update status %d: %s", rec.Code, rec.Body.String()) }
 var updated struct { Task Task `json:"task"` }
 if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil { t.Fatal(err) }
 if updated.Task.Title != "Updated" || updated.Task.Priority != "high" || updated.Task.DueDate == nil { t.Fatalf("unexpected updated task: %+v", updated.Task) }

 rec = httptest.NewRecorder()
 a.taskByID(rec, httptest.NewRequest(http.MethodDelete, "/api/tasks/"+created.Task.ID, nil))
 if rec.Code != http.StatusNoContent { t.Fatalf("delete status %d: %s", rec.Code, rec.Body.String()) }
 if got := len(a.store.snapshot()); got != 0 { t.Fatalf("expected no tasks after delete, got %d", got) }
}

func TestStrictJSONRejectsTrailingValue(t *testing.T) {
 a := testApp(t)
 rec := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`{"title":"one"} {"title":"two"}`))
 a.tasks(rec, req)
 if rec.Code != http.StatusBadRequest { t.Fatalf("expected 400 for multiple JSON values, got %d", rec.Code) }
}

func TestMCPToolDiscoveryAndCreate(t *testing.T) {
 a := testApp(t)
 reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
 rec := httptest.NewRecorder()
 a.mcp(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(reqBody)))
 if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "add_task") || !strings.Contains(rec.Body.String(), "complete_task") {
  t.Fatalf("MCP tool discovery failed: status=%d body=%s", rec.Code, rec.Body.String())
 }

 reqBody = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add_task","arguments":{"title":"MCP test task","priority":"high"}}}`
 rec = httptest.NewRecorder()
 a.mcp(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(reqBody)))
 if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "MCP test task") {
  t.Fatalf("MCP add_task failed: status=%d body=%s", rec.Code, rec.Body.String())
 }
 if got := len(a.store.snapshot()); got != 1 { t.Fatalf("expected one task after MCP call, got %d", got) }
}

func TestAssistantTaskCommand(t *testing.T) {
 a := testApp(t)
 reqBody := `{"message":"add prepare launch priority high due 2026-10-30"}`
 rec := httptest.NewRecorder()
 a.assistant(rec, httptest.NewRequest(http.MethodPost, "/api/assistant", strings.NewReader(reqBody)))
 if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "task_created") {
  t.Fatalf("assistant task command failed: status=%d body=%s", rec.Code, rec.Body.String())
 }
 items := a.store.snapshot()
 if len(items) != 1 || items[0].Title != "prepare launch" || items[0].Priority != "high" || items[0].DueDate == nil || *items[0].DueDate != "2026-10-30" {
  t.Fatalf("unexpected task from assistant: %+v", items)
 }
}
