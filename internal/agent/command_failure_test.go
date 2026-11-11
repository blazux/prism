package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"prism/internal/docker"
	"prism/internal/ollama"
)

type commandFailureBackend struct{ calls int }

func (b *commandFailureBackend) Chat(_ context.Context, _ ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	if b.calls == 1 {
		out <- ollama.StreamEvent{ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{
			Name: "exec_command", Arguments: json.RawMessage(`{"command":"fixture"}`),
		}}}, Done: true, DoneReason: "tool_calls"}
		return
	}
	out <- ollama.StreamEvent{Content: "The command failed.", Done: true, DoneReason: "stop"}
}
func (*commandFailureBackend) Ping(context.Context) error                   { return nil }
func (*commandFailureBackend) ListModels(context.Context) ([]string, error) { return nil, nil }
func (*commandFailureBackend) ContextBudgetChars() int                      { return 0 }

func TestCommandFailurePreservesOutputAndError(t *testing.T) {
	for _, execErr := range []error{errors.New("exit status 7: fixture stderr"), context.DeadlineExceeded, context.Canceled} {
		t.Run(execErr.Error(), func(t *testing.T) {
			e := NewToolExecutor(docker.WithExecution(func(context.Context, string, []byte, map[string]string) (string, error) {
				return "fixture stdout", execErr
			}), t.TempDir(), t.TempDir(), "", "")
			output, _, err := e.Execute(t.Context(), "exec_command", json.RawMessage(`{"command":"fixture"}`))
			if !errors.Is(err, execErr) || output != "fixture stdout" {
				t.Fatalf("execution lost status/output: %q %v", output, err)
			}
			a := New(&commandFailureBackend{}, e, "fixture", nil, "")
			events := make(chan Event, 100)
			a.Chat(t.Context(), "Run the command", nil, events)
			close(events)
			found := false
			for event := range events {
				if event.Type != "tool_result" {
					continue
				}
				found = true
				if !event.IsError || !strings.Contains(event.Output, "fixture stdout") || !strings.Contains(event.Output, execErr.Error()) {
					t.Fatalf("command failure misreported: %+v", event)
				}
			}
			if !found {
				t.Fatal("no command result")
			}
		})
	}
}
