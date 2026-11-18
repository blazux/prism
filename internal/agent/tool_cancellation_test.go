package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/ollama"
)

type cancellationBatchBackend struct {
	capturingBackend
	calls int
	tools []ollama.ToolCall
}

func (b *cancellationBatchBackend) Chat(_ context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	b.req = req
	if b.calls == 1 {
		out <- ollama.StreamEvent{ToolCalls: b.tools, Done: true, DoneReason: "tool_calls"}
		return
	}
	out <- ollama.StreamEvent{Content: "The previous turn was stopped.", Done: true, DoneReason: "stop"}
}

func cancelledWrite() ollama.ToolCall {
	return ollama.ToolCall{Function: ollama.ToolCallFunction{
		Name: "write_file", Arguments: json.RawMessage(`{"path":"after-stop.txt","content":"must not be written"}`),
	}}
}

func assertNoCancelledWrite(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "after-stop.txt")); !os.IsNotExist(err) {
		t.Fatalf("cancelled tool mutated the workspace: %v", err)
	}
}

func TestToolExecutorCancellationPreventsSynchronousWrite(t *testing.T) {
	for _, stage := range []string{"entry", "tool guard", "resource guard", "allowed"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			e := NewToolExecutor(nil, root, t.TempDir(), "", "")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch stage {
			case "entry":
				cancel()
			case "tool guard":
				e.SetToolGuard(func(string, map[string]interface{}) error { cancel(); return nil })
			case "resource guard":
				e.SetResourceMutationGuard(func() error { cancel(); return nil })
			}
			call := cancelledWrite()
			_, _, err := e.Execute(ctx, call.Function.Name, call.Function.Arguments)
			if stage == "allowed" {
				if err != nil {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(filepath.Join(root, "after-stop.txt")); err != nil || string(data) != "must not be written" {
					t.Fatalf("allowed write failed: %q, %v", data, err)
				}
				return
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			assertNoCancelledWrite(t, root)
		})
	}
}

func stoppedBatchFixture(t *testing.T) (*Agent, *cancellationBatchBackend, context.Context, *int) {
	t.Helper()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	e := NewToolExecutor(nil, root, t.TempDir(), "", "")
	relayed := new(int)
	e.SetTelephony(TelephonyTools, func(string, map[string]interface{}) (string, error) {
		(*relayed)++
		cancel()
		return "Message delivered once.", nil
	})
	b := &cancellationBatchBackend{tools: []ollama.ToolCall{
		{Function: ollama.ToolCallFunction{Name: "take_message", Arguments: json.RawMessage(`{"content":"fixture"}`)}},
		cancelledWrite(),
		{Function: ollama.ToolCallFunction{Name: "update_system_prompt", Arguments: json.RawMessage(`{"personality":"must not replace the personality"}`)}},
	}}
	a := New(b, e, "fixture", nil, "Original personality")
	return a, b, ctx, relayed
}

func assertStoppedBatch(t *testing.T, history []ollama.Message) {
	t.Helper()
	if len(history) != 5 || history[0].Role != "user" || history[1].Role != "assistant" || len(history[1].ToolCalls) != 3 {
		t.Fatalf("assistant batch missing from history: %+v", history)
	}
	for i, message := range history[2:] {
		if message.Role != "tool" {
			t.Fatalf("tool result %d missing: %+v", i, message)
		}
		if i == 0 && message.Content != "Message delivered once." {
			t.Fatalf("completed action result lost: %q", message.Content)
		}
		if i > 0 && !strings.Contains(message.Content, "cancelled before execution") {
			t.Fatalf("unexecuted action not marked cancelled: %q", message.Content)
		}
	}
}

func TestCancellationCompletesToolBatchWithoutLaterMutationOrReplay(t *testing.T) {
	a, backend, ctx, relayed := stoppedBatchFixture(t)
	events := make(chan Event, 100)
	a.Chat(ctx, "Run the batch", nil, events)
	assertStoppedBatch(t, a.history)
	assertNoCancelledWrite(t, a.executor.WorkspaceDir())
	if a.personality != "Original personality" || *relayed != 1 || backend.calls != 1 {
		t.Fatalf("cancelled batch continued: personality=%q relay=%d model=%d", a.personality, *relayed, backend.calls)
	}
	close(events)
	results, ends := 0, 0
	for event := range events {
		if event.Type == "tool_result" {
			if event.IsError != (results > 0) {
				t.Fatalf("wrong tool result status: %+v", event)
			}
			results++
		}
		if event.Type == "stream_end" {
			ends++
		}
	}
	if results != 3 || ends != 1 {
		t.Fatalf("incomplete terminal events: results=%d ends=%d", results, ends)
	}
	a.Chat(t.Context(), "Continue", nil, make(chan Event, 100))
	assertStoppedBatch(t, backend.req.Messages[1:6]) // Exclude the leading system prompt.
	assertNoCancelledWrite(t, a.executor.WorkspaceDir())
	if *relayed != 1 || backend.calls != 2 {
		t.Fatal("follow-up replayed a previous action")
	}
}

func TestCancellationDuringApprovalDoesNotExecuteTool(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b := &cancellationBatchBackend{tools: []ollama.ToolCall{cancelledWrite()}}
	a := New(b, NewToolExecutor(nil, root, t.TempDir(), "", ""), "fixture", nil, "")
	a.SetApprovalFns(func() bool { return true }, func(context.Context, string) bool { cancel(); return true })
	a.Chat(ctx, "Write", nil, make(chan Event, 100))
	assertNoCancelledWrite(t, root)
	if len(a.history) != 3 || !strings.Contains(a.history[2].Content, "cancelled before execution") {
		t.Fatalf("approval cancellation left an unanswered tool: %+v", a.history)
	}
}

func TestCancellationDuringSpecialToolAuthorizationDoesNotMutatePersonality(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetToolGuard(func(name string, args map[string]interface{}) error {
		if name == "update_system_prompt" && args != nil {
			cancel()
		}
		return nil
	})
	b := &cancellationBatchBackend{tools: []ollama.ToolCall{{Function: ollama.ToolCallFunction{
		Name: "update_system_prompt", Arguments: json.RawMessage(`{"personality":"must not replace the personality"}`),
	}}}}
	a := New(b, e, "fixture", nil, "Original personality")
	a.Chat(ctx, "Update", nil, make(chan Event, 100))
	if a.personality != "Original personality" || len(a.history) != 3 || !strings.Contains(a.history[2].Content, "cancelled before execution") {
		t.Fatalf("special tool bypassed cancellation: personality=%q history=%+v", a.personality, a.history)
	}
}

func TestCancelledToolBatchPersistsCompleteResultsForReload(t *testing.T) {
	store := resourceDatabase(t)
	a, _, ctx, relayed := stoppedBatchFixture(t)
	a.memStore = store
	a.Chat(ctx, "Run the batch", nil, make(chan Event, 100))
	for _, message := range a.history {
		if message.DBID == 0 {
			t.Fatalf("cancelled tool batch message was not persisted: %+v", message)
		}
	}
	reloaded := New(&capturingBackend{reply: "The turn was stopped."}, a.executor, "fixture", store, "")
	reloaded.loadHistoryFromDB(t.Context())
	assertStoppedBatch(t, reloaded.history)
	if *relayed != 1 {
		t.Fatal("history reload executed an action")
	}
}
