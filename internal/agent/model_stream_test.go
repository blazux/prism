package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"prism/internal/ollama"
)

type contractStreamBackend struct {
	capturingBackend
	events    []ollama.StreamEvent
	cancelled chan struct{}
}

func (b *contractStreamBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.req = req
	for _, event := range b.events {
		out <- event
	}
	if b.cancelled != nil {
		<-ctx.Done()
		close(b.cancelled)
	}
}

func TestModelStreamPreservesRequestAndProviderPayload(t *testing.T) {
	in, output := int64(71), int64(12)
	usage := &ollama.Usage{InputTokens: &in, OutputTokens: &output}
	tool := unfinishedTool()
	blocks := []json.RawMessage{json.RawMessage(`{"type":"thinking","signature":"opaque-provider-data"}`)}
	backend := &contractStreamBackend{events: []ollama.StreamEvent{
		{Thinking: "Inspect ", Content: "First "},
		{Thinking: "then act", Content: "second", ToolCalls: []ollama.ToolCall{tool}},
		{ProviderBlocks: blocks, Usage: usage, Done: true, DoneReason: "tool_calls"},
	}}
	req := ollama.ChatRequest{
		Model: "contract", TaskID: "parent-task", CacheSystemPrefixBytes: 20, CacheToolPrefixCount: 1,
		Messages:   []ollama.Message{{Role: "tool", Content: "capture", Images: []string{"image-data"}}},
		Tools:      []ollama.Tool{{Function: ollama.ToolFunction{Name: "fixture"}}},
		NoThinking: false, ReasoningEffort: "low", Options: ollama.Options{NumPredict: 65000},
	}
	var observed []ollama.StreamEvent
	result, err := collectModelStream(t.Context(), backend, req, func(ev ollama.StreamEvent) { observed = append(observed, ev) })
	if err != nil || result.Content != "First second" || result.DoneReason != "tool_calls" || !result.Done || !result.Complete {
		t.Fatalf("stream contract changed: %+v %v", result, err)
	}
	if !reflect.DeepEqual(req, backend.req) || !reflect.DeepEqual(observed, backend.events) ||
		!reflect.DeepEqual(result.ToolCalls, []ollama.ToolCall{tool}) || !reflect.DeepEqual(result.ProviderBlocks, blocks) || result.Usage != usage {
		t.Fatal("request, callback events, tools, provider blocks or usage were lost")
	}
}

func TestModelStreamCancelsProviderAfterError(t *testing.T) {
	failure := errors.New("provider error")
	backend := &contractStreamBackend{events: []ollama.StreamEvent{{Content: "partial"}, {Content: "ignored", Err: failure}}, cancelled: make(chan struct{})}
	result, err := collectModelStream(t.Context(), backend, ollama.ChatRequest{}, nil)
	if !errors.Is(err, failure) || result.Content != "partial" || result.Done {
		t.Fatalf("failed stream contract changed: %+v %v", result, err)
	}
	select {
	case <-backend.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not receive cancellation")
	}
}

func TestModelStreamIgnoresLatePayloadButKeepsUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	in := int64(99)
	usage := &ollama.Usage{InputTokens: &in}
	backend := &contractStreamBackend{events: []ollama.StreamEvent{
		{Content: "accepted"},
		{Content: "late", Thinking: "late", ToolCalls: []ollama.ToolCall{unfinishedTool()}, ProviderBlocks: []json.RawMessage{json.RawMessage(`{}`)}, Usage: usage, Done: true, DoneReason: "stop"},
	}}
	callbacks := 0
	result, err := collectModelStream(ctx, backend, ollama.ChatRequest{}, func(ollama.StreamEvent) { callbacks++; cancel() })
	if !errors.Is(err, context.Canceled) || result.Content != "accepted" || callbacks != 1 || len(result.ToolCalls) != 0 || len(result.ProviderBlocks) != 0 || result.Usage != usage {
		t.Fatalf("late payload accepted or usage lost: %+v callbacks=%d err=%v", result, callbacks, err)
	}
}

func TestModelStreamTerminalStatesRemainDistinct(t *testing.T) {
	for _, reason := range []string{"stop", "end_turn", "stop_sequence", "tool_calls", "length", "max_tokens", "interrupted", ""} {
		t.Run(reason, func(t *testing.T) {
			backend := &contractStreamBackend{events: []ollama.StreamEvent{{Content: "text", Done: true, DoneReason: reason}}}
			result, err := collectModelStream(t.Context(), backend, ollama.ChatRequest{}, nil)
			if err != nil || !result.Done || result.DoneReason != reason || result.Complete != (reason != "interrupted") {
				t.Fatalf("terminal state changed: %+v %v", result, err)
			}
		})
	}
}

func TestResearchCollectorPreservesTextOnlyContract(t *testing.T) {
	backend := &contractStreamBackend{events: []ollama.StreamEvent{{Thinking: "private", Content: " <think>private</think> Report "}, {Done: true, DoneReason: "stop"}}}
	e := &ToolExecutor{backend: backend, model: "fixture"}
	out, err := e.chatOnce(t.Context(), "instructions", "question", 0.3)
	if err != nil || out != "Report" || backend.req.Options.Temperature != 0.3 || len(backend.req.Messages) != 2 || backend.req.Model != "fixture" {
		t.Fatalf("research request or output changed: %q %v %+v", out, err, backend.req)
	}
	backend.events = []ollama.StreamEvent{{Content: "partial"}, {Err: errors.New("fixture failure")}}
	if out, err := e.chatOnce(t.Context(), "", "question", 0.2); err == nil || out != "" || !strings.Contains(err.Error(), "fixture failure") {
		t.Fatalf("research accepted a failed partial output: %q %v", out, err)
	}
}
