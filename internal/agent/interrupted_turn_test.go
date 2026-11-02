package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/ollama"
)

type partialTurnBackend struct {
	capturingBackend
	reason string
	calls  int
	empty  bool
}

func (b *partialTurnBackend) Chat(_ context.Context, _ ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	if !b.empty {
		out <- ollama.StreamEvent{Content: "Partial response."}
		out <- ollama.StreamEvent{ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "write_file", Arguments: json.RawMessage(`{"path":"must-not-exist.txt","content":"incomplete batch"}`)}}}}
	}
	out <- ollama.StreamEvent{Done: true, DoneReason: b.reason}
}

func TestInterruptedTurnKeepsPartialTextWithoutExecutingTools(t *testing.T) {
	for _, reason := range []string{"interrupted", "length", "max_tokens"} {
		t.Run(reason, func(t *testing.T) {
			root := t.TempDir()
			b := &partialTurnBackend{reason: reason}
			a := &Agent{ollama: b, executor: &ToolExecutor{workspaceDir: root}, sessionID: "fixture", limits: Limits{MaxIterations: 4}}
			events := make(chan Event, 30)
			a.Chat(t.Context(), "Write a report", nil, events)
			close(events)
			if b.calls != 1 {
				t.Fatal("interrupted generation was automatically rerun")
			}
			if _, err := os.Stat(filepath.Join(root, "must-not-exist.txt")); !os.IsNotExist(err) {
				t.Fatal("tool from an incomplete generation was executed")
			}
			last := a.history[len(a.history)-1]
			if last.Content != "Partial response." || len(last.ToolCalls) != 0 {
				t.Fatal("partial reply lost or unexecuted tool call entered history")
			}
			failures := 0
			for ev := range events {
				if ev.Type == "error" && strings.Contains(ev.Content, "partial reply") {
					failures++
				}
				if ev.Type == "tool_use" {
					t.Fatal("incomplete tool call advertised as executable")
				}
			}
			if failures != 1 {
				t.Fatalf("expected one visible failure, got %d", failures)
			}
		})
	}
}

func TestEmptyGenerationCannotStopSilently(t *testing.T) {
	for _, reason := range []string{"interrupted", "length", "stop"} {
		b := &partialTurnBackend{reason: reason, empty: true}
		a := &Agent{ollama: b, executor: &ToolExecutor{}, sessionID: "fixture", limits: Limits{MaxIterations: 4}}
		events := make(chan Event, 30)
		a.Chat(t.Context(), "Answer", nil, events)
		close(events)
		failures := 0
		for ev := range events {
			if ev.Type == "error" {
				failures++
			}
		}
		if failures != 1 {
			t.Errorf("%s: empty generation ended without a diagnostic", reason)
		}
	}
}
