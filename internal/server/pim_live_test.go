package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"prism/internal/memory"
)

// Opt in only with a disposable PostgreSQL database. Exercises the real SQL
// migration and the same HTTP/tool paths used by the UI and model.
func TestPIMUpgradeAndToolParityLive(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("requires disposable PRISM_TEST_POSTGRES_URL")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// The pre-change calendar schema, including a row written by that build.
	_, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS calendar_events (id BIGSERIAL PRIMARY KEY,session_id TEXT NOT NULL DEFAULT 'default',title TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',start_at TIMESTAMPTZ NOT NULL,end_at TIMESTAMPTZ,location TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO calendar_events(session_id,title,start_at) VALUES('upgrade-test','Legacy holiday','2026-09-20 00:00:00+00')`)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := memory.NewStore(ctx, dsn, bytes.Repeat([]byte{1}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	// Restart initialization is idempotent.
	again, err := memory.NewStore(ctx, dsn, bytes.Repeat([]byte{1}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	legacy, err := ms.ListEvents(ctx, "upgrade-test", nil, nil)
	if err != nil || len(legacy) == 0 || legacy[0].Title != "Legacy holiday" {
		t.Fatalf("legacy row: %+v %v", legacy, err)
	}
	dir := t.TempDir()
	s := New(Config{WorkspaceDir: dir, PluginDir: filepath.Join(dir, "plugins")})
	s.memStore = ms
	call := func(path, body string) map[string]any {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleBuiltinTool(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out["error"] != nil {
			t.Fatal(out)
		}
		return out
	}
	call("/api/builtin/task", `{"action":"add","title":"Parity task","priority":"high","due":"2026-09-21"}`)
	tasks, err := ms.ListTasks(ctx, "global", true)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, it := range tasks {
		if it.Title == "Parity task" {
			id = it.ID
		}
	}
	if id == 0 {
		t.Fatal("tool-created task missing from UI scope")
	}
	body, _ := json.Marshal(map[string]any{"action": "update", "id": id, "due": ""})
	call("/api/builtin/task", string(body))
	tasks, _ = ms.ListTasks(ctx, "global", true)
	for _, it := range tasks {
		if it.ID == id && (it.Title != "Parity task" || it.Priority != "high" || it.DueAt != nil) {
			t.Fatalf("partial edit lost fields: %+v", it)
		}
	}
	// Account scoping still applies in storage.
	if err := ms.PatchTask(ctx, "u999", id, nil, nil, true, nil); err == nil {
		t.Fatal("cross-user update succeeded")
	}
	call("/api/builtin/calendar", `{"action":"add","title":"Parity event","start":"2026-09-22","all_day":true}`)
	events, err := ms.ListEvents(ctx, "global", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var eid int64
	for _, it := range events {
		if it.Title == "Parity event" {
			eid = it.ID
			if it.AllDay == nil || !*it.AllDay || it.EndAt == nil {
				t.Fatalf("all-day did not persist: %+v", it)
			}
		}
	}
	body, _ = json.Marshal(map[string]any{"action": "update", "id": eid, "location": "Room B"})
	call("/api/builtin/calendar", string(body))
	w := httptest.NewRecorder()
	s.handleEvents(w, httptest.NewRequest("GET", "/api/events", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"allDay":true`) || !strings.Contains(w.Body.String(), "Room B") {
		t.Fatalf("UI cannot see tool changes: %s", w.Body.String())
	}
	// Actual upgrade data and test fixtures stay exclusively in the disposable DB.
}
