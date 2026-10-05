package agent

import (
	"context"
	"encoding/json"
	"testing"

	"prism/internal/ollama"
)

// Model selection creates another Agent for the same visible conversation.
// Its first tool result must still address its own tool block, not the first
// block left in the page by the previous Agent.
type oneToolBackend struct{ calls int }

func (b *oneToolBackend) Chat(_ context.Context, _ ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	if b.calls == 1 {
		out <- ollama.StreamEvent{ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{
			Name: "read_file", Arguments: json.RawMessage(`{"path":"missing-fixture"}`),
		}}}, Done: true, DoneReason: "tool_calls"}
		return
	}
	out <- ollama.StreamEvent{Content: "Done.", Done: true, DoneReason: "stop"}
}
func (*oneToolBackend) Ping(context.Context) error                   { return nil }
func (*oneToolBackend) ListModels(context.Context) ([]string, error) { return nil, nil }
func (*oneToolBackend) ContextBudgetChars() int                      { return 0 }

func TestToolEventIDsSurviveModelSwitch(t *testing.T) {
	var ids []string
	for _, model := range []string{"default", "secondary"} {
		a := New(&oneToolBackend{}, NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", ""), model, nil, "")
		a.SetSession("demo", "")
		events := make(chan Event, 32)
		a.Chat(t.Context(), "Read the fixture", nil, events)
		close(events)
		var useID, resultID string
		for ev := range events {
			switch ev.Type {
			case "tool_use":
				useID = ev.ID
			case "tool_result":
				resultID = ev.ID
			}
		}
		if useID == "" || resultID != useID {
			t.Fatalf("model %s: tool_use=%q tool_result=%q", model, useID, resultID)
		}
		ids = append(ids, useID)
	}
	if ids[0] == ids[1] {
		t.Fatalf("model switch reused a tool ID in the same conversation: %q", ids[0])
	}
}
