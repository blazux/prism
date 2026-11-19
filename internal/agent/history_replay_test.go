package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"prism/internal/anthropic"
	"prism/internal/ollama"
	"prism/internal/openai"
)

func recoveryBatch() ollama.Message {
	return ollama.Message{Role: "assistant", Content: "Preparing the files.", DBID: 2, ToolCalls: []ollama.ToolCall{
		{Function: ollama.ToolCallFunction{Name: "write_file", Arguments: json.RawMessage(`{"path":"completed.txt","content":"[REDACTED]"}`)}},
		{Function: ollama.ToolCallFunction{Name: "write_file", Arguments: json.RawMessage(`{"path":"pending.txt","content":"must not be replayed"}`)}},
	}}
}

func TestReplayCompletesMissingResultsWithoutChangingKnownMessages(t *testing.T) {
	user := ollama.Message{Role: "user", Content: "[2026-11-18 12:00] Prepare both files", DBID: 1}
	batch := recoveryBatch()
	first := ollama.Message{Role: "tool", Content: "Saved first result", DBID: 3, Images: []string{"image-output"}}
	second := ollama.Message{Role: "tool", Content: "Saved second result", DBID: 4}
	note := ollama.Message{Role: "user", Content: "[2026-11-18 12:01] A note changed", DBID: 5}
	next := ollama.Message{Role: "assistant", Content: "Previous turn ended", DBID: 6}
	nextBatch := recoveryBatch()
	nextBatch.Content, nextBatch.DBID = "A different batch", 6
	nextFirst, nextSecond := first, second
	nextFirst.DBID, nextSecond.DBID = 7, 8
	unknown := ollama.Message{Role: "tool", Content: unknownToolOutcome}
	for _, tc := range []struct {
		name    string
		input   []ollama.Message
		want    []ollama.Message
		missing int
	}{
		{"empty", nil, nil, 0},
		{"complete", []ollama.Message{user, batch, first, second, note}, []ollama.Message{user, batch, first, second, note}, 0},
		{"partial", []ollama.Message{user, batch, first, note}, []ollama.Message{user, batch, first, unknown, note}, 1},
		{"no results", []ollama.Message{user, batch, note}, []ollama.Message{user, batch, unknown, unknown, note}, 2},
		{"interleaved note", []ollama.Message{user, batch, note, first, second}, []ollama.Message{user, batch, first, second, note}, 0},
		{"next assistant bounds batch", []ollama.Message{user, batch, first, note, next}, []ollama.Message{user, batch, first, unknown, note, next}, 1},
		{"separate batches", []ollama.Message{user, batch, first, note, nextBatch, nextFirst, nextSecond}, []ollama.Message{user, batch, first, unknown, note, nextBatch, nextFirst, nextSecond}, 1},
		{"ordinary conversation", []ollama.Message{user, next, note}, []ollama.Message{user, next, note}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, _ := json.Marshal(tc.input)
			got, missing := normalizeReplayedToolBatches(tc.input)
			if !reflect.DeepEqual(got, tc.want) || missing != tc.missing {
				t.Fatalf("replay = %+v, missing=%d; want %+v, %d", got, missing, tc.want, tc.missing)
			}
			unchanged, _ := json.Marshal(tc.input)
			if string(unchanged) != string(original) {
				t.Fatal("normalization mutated the source messages or arguments")
			}
			again, missing := normalizeReplayedToolBatches(got)
			if !reflect.DeepEqual(got, again) || missing != 0 {
				t.Fatal("replaying normalized history duplicated recovery results")
			}
		})
	}
	for _, forbidden := range []string{"cancelled", "no action was taken", "not executed"} {
		if strings.Contains(unknownToolOutcome, forbidden) {
			t.Fatalf("unknown outcome claims non-execution: %q", unknownToolOutcome)
		}
	}
}

type recoveryCaptureBackend struct {
	capturingBackend
	calls int
}

func (b *recoveryCaptureBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	b.capturingBackend.Chat(ctx, req, out)
}

func TestReloadedUnfinishedBatchDoesNotReplayActionsOrRewriteArchive(t *testing.T) {
	store := resourceDatabase(t)
	ctx := t.Context()
	const session = "recovery-fixture"
	batch, _ := json.Marshal(recoveryBatch().ToolCalls)
	for _, row := range []struct {
		role, content string
		calls         json.RawMessage
	}{{"user", "Create the files. Original attached file: uploads/source.png", nil}, {"assistant", "Preparing the files.", batch}, {"user", "A concurrent note changed", nil}, {"tool", "Saved first result", nil}} {
		if _, err := store.AppendMessage(ctx, session, row.role, row.content, row.calls); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.LoadHistory(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "completed.txt"), []byte("keep this existing result"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := &recoveryCaptureBackend{capturingBackend: capturingBackend{reply: "Recovered state."}}
	ag := New(backend, NewToolExecutor(nil, root, t.TempDir(), "", ""), "fixture", store, "")
	ag.SetSession(session, "")
	ag.loadHistoryFromDB(ctx)
	loaded := append([]ollama.Message(nil), ag.history...)
	if backend.calls != 0 || len(loaded) != 5 || loaded[2].Content != "Saved first result" || loaded[3].Content != unknownToolOutcome || loaded[3].DBID != 0 || !strings.Contains(loaded[4].Content, "A concurrent note changed") {
		t.Fatalf("bad replay or automatic execution: calls=%d history=%+v", backend.calls, loaded)
	}
	ag.ReloadStoredHistory()
	ag.loadHistoryFromDB(ctx)
	if !reflect.DeepEqual(loaded, ag.history) {
		t.Fatal("reloading the same archive changed recovery results")
	}
	after, _ := store.LoadHistory(ctx, session)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("synthetic recovery wrote to the archive")
	}
	ag.Chat(ctx, "Continue after checking the current files", nil, make(chan Event, 100))
	if backend.calls != 1 {
		t.Fatalf("expected one new model request, got %d", backend.calls)
	}
	wire, _ := json.Marshal(backend.req.Messages)
	if !strings.Contains(string(wire), unknownToolOutcome) || !strings.Contains(string(wire), "uploads/source.png") || !strings.Contains(string(wire), "[REDACTED]") {
		t.Fatal("recovered context lost unknown status, file reference or redacted arguments")
	}
	if _, err := os.Stat(filepath.Join(root, "pending.txt")); !os.IsNotExist(err) {
		t.Fatal("an archived pending tool call was executed")
	}
	data, err := os.ReadFile(filepath.Join(root, "completed.txt"))
	if err != nil || string(data) != "keep this existing result" {
		t.Fatal("an archived completed action was replayed")
	}
	after, _ = store.LoadHistory(ctx, session)
	if len(after) != len(before)+2 || !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatal("new turn rewrote the saved transcript")
	}
	for _, row := range after {
		if strings.Contains(row.Content, unknownToolOutcome) {
			t.Fatal("synthetic outcome was persisted")
		}
	}
	// A writer can still finish after a fresh agent reads the unfinished span.
	const concurrent = "concurrent-recovery-fixture"
	for _, row := range []struct {
		role, content string
		calls         json.RawMessage
	}{{"user", "Work", nil}, {"assistant", "Preparing", batch}, {"tool", "Saved first result", nil}} {
		if _, err := store.AppendMessage(ctx, concurrent, row.role, row.content, row.calls); err != nil {
			t.Fatal(err)
		}
	}
	ag.SetSession(concurrent, "")
	ag.loadHistoryFromDB(ctx)
	if _, err := store.AppendMessage(ctx, concurrent, "tool", "Real second result from another worker", nil); err != nil {
		t.Fatal(err)
	}
	ag.ReloadStoredHistory()
	ag.loadHistoryFromDB(ctx)
	for _, message := range ag.history {
		if message.Content == unknownToolOutcome {
			t.Fatal("synthetic recovery hid the later real result")
		}
	}
	if ag.history[len(ag.history)-1].Content != "Real second result from another worker" {
		t.Fatal("real result missing after reload")
	}
}

func TestRecoveredBatchesReachStrictProviderAdapters(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
	history, _ := normalizeReplayedToolBatches([]ollama.Message{
		{Role: "user", Content: "Prepare both files"}, recoveryBatch(),
		{Role: "user", Content: "Concurrent user note"},
		{Role: "tool", Content: "Saved first result", Images: []string{png}},
	})
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			captured := make(chan map[string]interface{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					http.Error(w, "invalid JSON", 400)
					return
				}
				if err := validateRecoveredWire(provider, payload); err != nil {
					t.Error(err)
					http.Error(w, "invalid tool order", 400)
					return
				}
				captured <- payload
				w.Header().Set("Content-Type", "text/event-stream")
				if provider == "openai" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}
			}))
			defer server.Close()
			var backend ollama.Backend = openai.NewClient(server.URL, "fixture")
			if provider == "anthropic" {
				backend = anthropic.NewClient(server.URL, "sk-ant-api03-test", "fixture")
			}
			out := make(chan ollama.StreamEvent, 20)
			backend.Chat(t.Context(), ollama.ChatRequest{Model: "fixture", Messages: history, Options: ollama.Options{NumPredict: 64}}, out)
			for len(out) > 0 {
				if event := <-out; event.Err != nil {
					t.Fatal(event.Err)
				}
			}
			payload := <-captured
			wire, _ := json.Marshal(payload)
			for _, text := range []string{unknownToolOutcome, "Saved first result", "Concurrent user note", "[REDACTED]", png} {
				if !strings.Contains(string(wire), text) {
					t.Fatalf("%s lost recovery context %q", provider, text)
				}
			}
		})
	}
}

func validateRecoveredWire(provider string, payload map[string]interface{}) error {
	var pending []string
	uses, results := 0, 0
	for _, raw := range payload["messages"].([]interface{}) {
		message := raw.(map[string]interface{})
		if provider == "openai" {
			if message["role"] == "tool" {
				if len(pending) == 0 || message["tool_call_id"] != pending[0] {
					return fmt.Errorf("unmatched OpenAI result")
				}
				pending = pending[1:]
				results++
				continue
			}
			if len(pending) != 0 {
				return fmt.Errorf("OpenAI tool batch split by %s", message["role"])
			}
			if calls, ok := message["tool_calls"].([]interface{}); ok {
				for _, rawCall := range calls {
					pending = append(pending, rawCall.(map[string]interface{})["id"].(string))
					uses++
				}
			}
			continue
		}
		for _, rawBlock := range message["content"].([]interface{}) {
			block := rawBlock.(map[string]interface{})
			switch block["type"] {
			case "tool_use":
				pending = append(pending, block["id"].(string))
				uses++
			case "tool_result":
				if len(pending) == 0 || block["tool_use_id"] != pending[0] {
					return fmt.Errorf("unmatched Anthropic result")
				}
				pending = pending[1:]
				results++
			default:
				if message["role"] != "assistant" && len(pending) != 0 {
					return fmt.Errorf("Anthropic result batch split by %s", block["type"])
				}
			}
		}
	}
	if len(pending) != 0 || uses != 2 || results != 2 {
		return fmt.Errorf("incomplete provider batch: %d uses, %d results, %d pending", uses, results, len(pending))
	}
	return nil
}
