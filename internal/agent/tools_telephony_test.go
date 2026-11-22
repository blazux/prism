package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prism/internal/ollama"
)

func TestPhoneToolsScheduleReturnIDAndPreserveCreationIdentity(t *testing.T) {
	var posts int
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Prism-Scope") != `{"instance":"fixture","owner":7,"standard":false}` {
			t.Error("lost scoped identity")
		}
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `[{"name":"Plumber","phone_number":"+596596123456"},{"name":"Plumber office","phone_number":"+596596654321"}]`)
			return
		}
		posts++
		if r.URL.Path != "/api/calls" || json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid create request")
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":42,"status":"pending"}`)
	}))
	t.Cleanup(server.Close)
	e := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetVox(server.URL, "", "")
	e.SetVoxScope(`{"instance":"fixture","owner":7,"standard":false}`)
	var created QueuedCall
	e.SetCallQueuedCallback(func(ctx context.Context, call QueuedCall) error { created = call; return nil })
	date := time.Now().Add(time.Hour).Format(time.RFC3339)
	text, err := e.placeCall(t.Context(), map[string]any{"contact_name": "Plumber", "mission": "Book an appointment", "scheduled_at": date, "max_attempts": float64(4)})
	if err != nil {
		t.Fatal(err)
	}
	if posts != 1 || payload["phone_number"] != "+596596123456" || payload["scheduled_at"] != date || payload["max_attempts"] != float64(4) || payload["request_id"] == "" {
		t.Fatalf("wrong scheduling request: %v", payload)
	}
	if created.ID != 42 || created.VoxURL != server.URL || created.Scope != e.voxScope || !strings.Contains(text, `"automatic_result_tracking": true`) {
		t.Fatalf("creation identity or tracking lost: %+v %s", created, text)
	}
	e.SetCallQueuedCallback(func(context.Context, QueuedCall) error { return errors.New("database offline") })
	text, err = e.placeCall(t.Context(), map[string]any{"phone_number": "+596596123456", "mission": "Ask for a time"})
	if err != nil || posts != 2 || !strings.Contains(text, `"automatic_result_tracking": false`) || !strings.Contains(text, "do not place it again") {
		t.Fatal("tracking failure incorrectly rejected an accepted call", err, text)
	}
	for _, args := range []map[string]any{
		{"mission": "test", "phone_number": "123", "scheduled_at": "tomorrow"},
		{"mission": "test", "phone_number": "123", "max_attempts": 2.5},
		{"mission": "test", "phone_number": "123", "max_attempts": 11},
	} {
		if _, err := e.placeCall(t.Context(), args); err == nil {
			t.Fatal("invalid schedule/attempts reached the phone service")
		}
	}
	if posts != 2 {
		t.Fatal("invalid request originated a call")
	}
}

func TestPhoneToolsReadExactMissionsAndPaginate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/calls/42" {
			fmt.Fprint(w, `{"id":42,"status":"done","result_summary":"No confirmed appointment"}`)
			return
		}
		if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("offset") != "2" {
			t.Error("pagination not forwarded")
		}
		fmt.Fprint(w, `{"total":5,"offset":2,"items":[{"id":3},{"id":4}]}`)
	}))
	t.Cleanup(server.Close)
	e := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetVox(server.URL, "", "")
	text, err := e.listCalls(t.Context(), map[string]any{"call_id": float64(42)})
	if err != nil || !strings.Contains(text, "No confirmed appointment") {
		t.Fatal(err, text)
	}
	text, err = e.listCalls(t.Context(), map[string]any{"limit": 2, "offset": 2})
	if err != nil || !strings.Contains(text, `"next_offset": 4`) {
		t.Fatal(err, text)
	}
	if _, err := e.cancelCall(t.Context(), map[string]any{"call_id": 42.5}); err == nil {
		t.Fatal("fractional ID could cancel a different mission")
	}
}

func TestCallHistoryKeepsScopeFiltersAndDetailPagination(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		if r.URL.Path != "/api/history" || q.Get("kind") != "calls" || q.Get("id") != "8" || q.Get("section") != "tools" || q.Get("text_offset") != "400" || q.Get("number") != "+596 596 12 34 56" || q.Get("admin") != "" || q.Get("owner") != "" {
			t.Errorf("incorrect scoped archive request: %s", r.URL)
		}
		if r.Header.Get("X-Prism-Scope") != `{"instance":"fixture","owner":7,"standard":false}` {
			t.Error("archive identity changed")
		}
		fmt.Fprint(w, `{"record_text":"part","next_text_offset":600,"next_offset":null}`)
	}))
	t.Cleanup(server.Close)
	e := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetVox(server.URL, "", "")
	e.SetVoxScope(`{"instance":"fixture","owner":7,"standard":false}`)
	text, err := e.callHistory(t.Context(), map[string]any{"id": 8, "section": "tools", "text_offset": 400, "number": "+596 596 12 34 56", "admin": true, "owner": 99})
	if err != nil || !strings.Contains(text, `"next_text_offset":600`) {
		t.Fatal(err, text)
	}
	for _, args := range []map[string]any{{"since": "today"}, {"limit": 51}, {"number": "123"}, {"direction": "all"}, {"offset": -1}} {
		if _, err := e.callHistory(t.Context(), args); err == nil {
			t.Fatal("invalid archive filter was accepted")
		}
	}
	e.SetVoxScope(`{"instance":"fixture","standard":false}`)
	if _, err := e.callHistory(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatal("unauthorized group could read archive", err)
	}
	if requests != 1 {
		t.Fatal("denied or malformed request reached archive")
	}
}

func TestPersistedEventsWaitForTurnBoundaryAndDeduplicate(t *testing.T) {
	a := &Agent{historyLoaded: true, history: []ollama.Message{{DBID: 1, Role: "assistant", ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "note"}}}}}}
	a.ObservePersistedMessage(3, "assistant", "Call finished")
	a.ObservePersistedMessage(3, "assistant", "Call finished")
	if len(a.history) != 1 {
		t.Fatal("external result interrupted a live tool batch")
	}
	a.history = append(a.history, ollama.Message{DBID: 2, Role: "tool", Content: "result"})
	a.mergePersistedMessages()
	if len(a.history) != 3 || a.history[1].Role != "tool" || a.history[2].DBID != 3 {
		t.Fatal("saved event was lost or broke tool-result order")
	}
	a.ObservePersistedMessage(3, "assistant", "Call finished")
	a.mergePersistedMessages()
	if len(a.history) != 3 {
		t.Fatal("saved event was duplicated after history replay")
	}
}
