package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"prism/internal/agent"
	"prism/internal/ollama"
)

type persistenceStreamBackend struct {
	fail    bool
	request ollama.ChatRequest
}

func (b *persistenceStreamBackend) Chat(_ context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.request = req
	if b.fail {
		out <- ollama.StreamEvent{Content: "Persisted partial report."}
		out <- ollama.StreamEvent{ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{
			Name: "write_file", Arguments: json.RawMessage(`{"path":"must-not-exist.txt","content":"unfinished tool batch"}`),
		}}}}
		out <- ollama.StreamEvent{Err: errors.New("fixture stream disconnected")}
		return
	}
	out <- ollama.StreamEvent{Content: "Continuing from the partial report.", Done: true, DoneReason: "stop"}
}
func (*persistenceStreamBackend) Ping(context.Context) error                   { return nil }
func (*persistenceStreamBackend) ListModels(context.Context) ([]string, error) { return nil, nil }
func (*persistenceStreamBackend) ContextBudgetChars() int                      { return 0 }

func TestStreamFailurePartialReplySurvivesReconnection(t *testing.T) {
	store := securityStore(t)
	root, plugins := t.TempDir(), t.TempDir()
	const session = "partial-stream-persistence"
	makeAgent := func(backend ollama.Backend) *agent.Agent {
		executor := agent.NewToolExecutor(nil, root, plugins, "", "")
		executor.SetSessionID(session)
		executor.SetMemoryStore(store)
		a := agent.New(backend, executor, "fixture", store, "")
		a.SetSession(session, "")
		return a
	}
	failed := makeAgent(&persistenceStreamBackend{fail: true})
	events := make(chan agent.Event, 128)
	failed.Chat(t.Context(), "Write a report", nil, events)
	close(events)
	for event := range events {
		if event.Type == "tool_use" {
			t.Fatal("unfinished tool batch was advertised for execution")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "must-not-exist.txt")); !os.IsNotExist(err) {
		t.Fatal("unfinished tool batch modified the workspace")
	}
	archive, err := store.LoadHistory(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive) != 2 || archive[0].Role != "user" || archive[1].Role != "assistant" || archive[1].Content != "Persisted partial report." {
		t.Fatalf("partial reply missing from durable archive: %+v", archive)
	}
	for _, row := range archive {
		var calls []ollama.ToolCall
		if len(row.ToolCalls) > 0 {
			if err := json.Unmarshal(row.ToolCalls, &calls); err != nil {
				t.Fatal(err)
			}
		}
		if len(calls) != 0 {
			t.Fatal("unfinished tool calls entered the archive")
		}
	}

	// Rebuild the agent as a new connection/headless request does; no in-memory
	// conversation from the failed instance is available to this one.
	followup := &persistenceStreamBackend{}
	reconnected := makeAgent(followup)
	events = make(chan agent.Event, 128)
	reconnected.Chat(t.Context(), "Continue", nil, events)
	close(events)
	found := false
	for _, message := range followup.request.Messages {
		if message.Role == "assistant" && message.Content == "Persisted partial report." {
			found = true
		}
		if len(message.ToolCalls) != 0 || message.Role == "tool" {
			t.Fatal("reconnection replayed the unfinished tool batch")
		}
	}
	if !found {
		t.Fatal("new agent did not reload the partial reply from PostgreSQL")
	}
	for event := range events {
		if event.Type == "error" || event.Type == "tool_use" {
			t.Fatalf("unexpected follow-up event: %+v", event)
		}
	}
}
