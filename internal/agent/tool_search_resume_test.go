package agent

import (
	"context"
	"encoding/json"
	"testing"

	"prism/internal/ollama"
)

type searchPauseBackend struct {
	capturingBackend
	requests    []ollama.ChatRequest
	alwaysPause bool
}

func (b *searchPauseBackend) Chat(_ context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.requests = append(b.requests, req)
	if len(b.requests) == 1 || b.alwaysPause {
		out <- ollama.StreamEvent{Done: true, DoneReason: "pause_turn", ProviderBlocks: []json.RawMessage{
			json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_fixture","name":"tool_search_tool_bm25","input":{"query":"list files"}}`),
		}}
		return
	}
	out <- ollama.StreamEvent{Content: "Found the requested capability.", Done: true, DoneReason: "end_turn"}
}

func TestToolSearchPauseResumesWithoutSyntheticUserMessage(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		b := &searchPauseBackend{alwaysPause: bounded}
		a := &Agent{ollama: b, executor: &ToolExecutor{}, sessionID: "fixture", limitsOverride: Limits{PromptProfile: "minimal", MaxIterations: MinMaxIterations}}
		events := make(chan Event, 100)
		a.Chat(t.Context(), "Find the file tool", nil, events)
		want := 2
		if bounded {
			want = MinMaxIterations
		}
		if len(b.requests) != want {
			t.Fatalf("pause loop made %d calls, want %d", len(b.requests), want)
		}
		for _, req := range b.requests[1:] {
			last := req.Messages[len(req.Messages)-1]
			if last.Role != "assistant" || len(last.ProviderBlocks) == 0 {
				t.Fatalf("server pause replaced with a user nudge: %+v", last)
			}
			for _, msg := range req.Messages {
				if msg.Role == "tool" || msg.Content == "continue" {
					t.Fatal("server tool was executed or synthetic nudge inserted")
				}
			}
		}
	}
}
