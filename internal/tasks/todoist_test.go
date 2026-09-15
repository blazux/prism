package tasks

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// "low" must round-trip distinctly from "normal" — they used to both write
// as Todoist priority 1, silently collapsing an explicit "low" into "normal"
// with no way to recover it on the next read.
func TestTodoistPriorityRoundTrip(t *testing.T) {
	for _, p := range []string{"high", "normal", "low"} {
		got := fromTodoistPriority(toTodoistPriority(p))
		if got != p {
			t.Errorf("round-trip %q: toTodoistPriority=%d, fromTodoistPriority=%q, want %q",
				p, toTodoistPriority(p), got, p)
		}
	}
	if toTodoistPriority("low") == toTodoistPriority("normal") {
		t.Fatal("low and normal must map to distinct Todoist priority values")
	}
}

// Todoist sends a floating datetime for a task with no timezone. Branching on
// the field being present rather than on the parse succeeding dropped the due
// date entirely, and the task stopped looking urgent.
func TestFloatingDueDateFallsBackToThePlainDate(t *testing.T) {
	const body = `[
	 {"id":"1","content":"flottante","due":{"date":"2026-03-02","datetime":"2026-03-02T09:00:00"}},
	 {"id":"2","content":"zonee","due":{"date":"2026-03-02","datetime":"2026-03-02T09:00:00Z"}},
	 {"id":"3","content":"jour","due":{"date":"2026-03-02"}},
	 {"id":"4","content":"illisible","due":{"date":"pas-une-date","datetime":"n importe quoi"}}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks" {
			t.Errorf("wrong endpoint %s", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	p := &TodoistProvider{token: "x", baseURL: srv.URL}
	items, err := p.List(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d items", len(items))
	}
	for _, it := range items[:3] {
		if it.DueAt == nil {
			t.Errorf("%q lost its due date", it.Title)
			continue
		}
		if y, m, d := it.DueAt.Date(); y != 2026 || m != time.March || d != 2 {
			t.Errorf("%q due %s", it.Title, it.DueAt)
		}
	}
	if items[3].DueAt != nil {
		t.Errorf("an unreadable due date invented one: %s", items[3].DueAt)
	}
}
