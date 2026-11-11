package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prism/internal/ollama"
)

type failingStreamBackend struct {
	capturingBackend
	stream func(context.Context, chan<- ollama.StreamEvent)
}

func (b *failingStreamBackend) Chat(ctx context.Context, _ ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.stream(ctx, out)
}

func unfinishedTool() ollama.ToolCall {
	return ollama.ToolCall{Function: ollama.ToolCallFunction{Name: "write_file", Arguments: json.RawMessage(`{"path":"must-not-exist.txt","content":"incomplete"}`)}}
}

func TestStreamErrorKeepsPartialReplyWithoutExecutingTools(t *testing.T) {
	root := t.TempDir()
	b := &failingStreamBackend{stream: func(_ context.Context, out chan<- ollama.StreamEvent) {
		out <- ollama.StreamEvent{Thinking: "working"}
		out <- ollama.StreamEvent{Content: "Partial response.", ToolCalls: []ollama.ToolCall{unfinishedTool()}}
		out <- ollama.StreamEvent{Err: errors.New("fixture stream failure")}
	}}
	a := New(b, NewToolExecutor(nil, root, t.TempDir(), "", ""), "fixture", nil, "")
	events := make(chan Event, 100)
	a.Chat(t.Context(), "Write a report", nil, events)
	close(events)
	assertPartialWithoutTool(t, a, root)
	failures, endings := 0, 0
	for event := range events {
		switch event.Type {
		case "error":
			failures++
			if !strings.Contains(event.Content, "fixture stream failure") {
				t.Fatalf("actual error lost: %q", event.Content)
			}
		case "stream_end":
			endings++
		case "tool_use":
			t.Fatal("tool from failed stream was advertised for execution")
		}
	}
	if failures != 1 || endings != 1 {
		t.Fatalf("terminal events errors=%d endings=%d", failures, endings)
	}
	followup := &stoppingBackend{capturingBackend: capturingBackend{reply: "Continuing."}}
	a.ollama = followup
	a.Chat(t.Context(), "Continue", nil, make(chan Event, 100))
	found := false
	for _, message := range followup.req.Messages {
		found = found || (message.Role == "assistant" && message.Content == "Partial response." && len(message.ToolCalls) == 0)
	}
	if !found {
		t.Fatal("follow-up lost the partial reply")
	}
}

func TestCancellationKeepsOnlyAlreadyStreamedText(t *testing.T) {
	root := t.TempDir()
	b := &failingStreamBackend{stream: func(ctx context.Context, out chan<- ollama.StreamEvent) {
		out <- ollama.StreamEvent{Content: "Partial response.", ToolCalls: []ollama.ToolCall{unfinishedTool()}}
		<-ctx.Done()
		out <- ollama.StreamEvent{Content: "Late text must be ignored.", Done: true, DoneReason: "stop"}
	}}
	a := New(b, NewToolExecutor(nil, root, t.TempDir(), "", ""), "fixture", nil, "")
	events := make(chan Event, 100)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		a.Chat(ctx, "Write a report", nil, events)
		close(events)
		close(done)
	}()
	for {
		select {
		case event := <-events:
			if event.Type == "stream" && event.Content == "Partial response." {
				cancel()
				goto cancelled
			}
		case <-time.After(3 * time.Second):
			t.Fatal("partial text did not stream")
		}
	}

cancelled:
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled turn did not finish")
	}
	assertPartialWithoutTool(t, a, root)
	endings := 0
	for event := range events {
		if event.Type == "error" || event.Type == "tool_use" || strings.Contains(event.Content, "Late text") {
			t.Fatalf("unexpected event after cancellation: %+v", event)
		}
		if event.Type == "stream_end" {
			endings++
		}
	}
	if endings != 1 {
		t.Fatalf("stream endings=%d", endings)
	}
}

func assertPartialWithoutTool(t *testing.T, a *Agent, root string) {
	t.Helper()
	last := a.history[len(a.history)-1]
	if last.Role != "assistant" || last.Content != "Partial response." || len(last.ToolCalls) != 0 {
		t.Fatalf("partial reply lost or incomplete tool calls persisted: %+v", last)
	}
	if _, err := os.Stat(filepath.Join(root, "must-not-exist.txt")); !os.IsNotExist(err) {
		t.Fatal("incomplete tool call executed")
	}
}
