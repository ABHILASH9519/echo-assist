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
