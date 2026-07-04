package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"prism/internal/ollama"
)

// sseServer returns an httptest server that streams the given raw SSE lines.
func sseServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			w.Write([]byte(l + "\n"))
		}
	}))
}

func drain(ch <-chan ollama.StreamEvent) (content, thinking string, calls []ollama.ToolCall, err error) {
	var sb, tb strings.Builder
	for ev := range ch {
		if ev.Err != nil {
			err = ev.Err
		}
		sb.WriteString(ev.Content)
		tb.WriteString(ev.Thinking)
		if len(ev.ToolCalls) > 0 {
			calls = append(calls, ev.ToolCalls...)
		}
	}
	return sb.String(), tb.String(), calls, err
}

func TestChat_ContentAndReasoning(t *testing.T) {
	srv := sseServer(t, []string{
		`data: {"choices":[{"delta":{"reasoning_content":"hmm "}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"let me think"}}]}`,
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":", world"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	ch := make(chan ollama.StreamEvent, 50)
	go func() { c.Chat(context.Background(), ollama.ChatRequest{Model: "m"}, ch); close(ch) }()

	content, thinking, calls, err := drain(ch)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if content != "Hello, world" {
		t.Errorf("content = %q, want %q", content, "Hello, world")
	}
	if thinking != "hmm let me think" {
		t.Errorf("thinking = %q, want %q", thinking, "hmm let me think")
	}
	if len(calls) != 0 {
		t.Errorf("unexpected tool calls: %v", calls)
	}
}

// Some vLLM builds (e.g. Qwen3.5 + DFlash on the DGX Spark) stream the thinking
// tokens in a "reasoning" field rather than "reasoning_content".
func TestChat_ReasoningFieldVariant(t *testing.T) {
	srv := sseServer(t, []string{
		`data: {"choices":[{"delta":{"reasoning":"think "}}]}`,
		`data: {"choices":[{"delta":{"reasoning":"hard"}}]}`,
		`data: {"choices":[{"delta":{"content":"42"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	ch := make(chan ollama.StreamEvent, 50)
	go func() { c.Chat(context.Background(), ollama.ChatRequest{Model: "m"}, ch); close(ch) }()

	content, thinking, _, err := drain(ch)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if content != "42" {
		t.Errorf("content = %q, want %q", content, "42")
	}
	if thinking != "think hard" {
		t.Errorf("thinking = %q, want %q", thinking, "think hard")
	}
}

// A transient failure at the start of a turn (503 / connection blip) must be
// retried before any token is streamed — a flaky link to the backend shouldn't
// abort the request.
func TestChat_RetriesTransientThenSucceeds(t *testing.T) {
	old := retryBaseBackoff
	retryBaseBackoff = time.Millisecond
	defer func() { retryBaseBackoff = old }()

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n < 2 { // fail the first two attempts
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n"))
		w.Write([]byte("data: [DONE]\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	ch := make(chan ollama.StreamEvent, 50)
	go func() { c.Chat(context.Background(), ollama.ChatRequest{Model: "m"}, ch); close(ch) }()

	content, _, _, err := drain(ch)
	if err != nil {
		t.Fatalf("expected success after retries, got err: %v", err)
	}
	if content != "ok" {
		t.Errorf("content = %q, want %q", content, "ok")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("server calls = %d, want 3 (2 transient + 1 success)", calls)
	}
}

func TestChat_FragmentedToolCall(t *testing.T) {
	// A single tool call whose name and JSON arguments are split across deltas.
	srv := sseServer(t, []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"get_weather"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	ch := make(chan ollama.StreamEvent, 50)
	go func() { c.Chat(context.Background(), ollama.ChatRequest{Model: "m"}, ch); close(ch) }()

	_, _, calls, err := drain(ch)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].Function.Name != "get_weather" {
		t.Errorf("name = %q, want get_weather", calls[0].Function.Name)
	}
	var args struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(calls[0].Function.Arguments, &args); err != nil {
		t.Fatalf("arguments not valid JSON (%q): %v", calls[0].Function.Arguments, err)
	}
	if args.City != "Paris" {
		t.Errorf("city = %q, want Paris", args.City)
	}
}

func TestBuildMessages_ToolCallIDCorrelation(t *testing.T) {
	in := []ollama.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{
			{Function: ollama.ToolCallFunction{Name: "a", Arguments: json.RawMessage(`{}`)}},
			{Function: ollama.ToolCallFunction{Name: "b", Arguments: json.RawMessage(`{}`)}},
		}},
		{Role: "tool", Content: "res-a"},
		{Role: "tool", Content: "res-b"},
	}
	out := buildMessages(in)

	// Find the assistant message and the two tool messages.
	var asst chatMsg
	var tools []chatMsg
	for _, m := range out {
		switch m.Role {
		case "assistant":
			asst = m
		case "tool":
			tools = append(tools, m)
		}
	}
	if len(asst.ToolCalls) != 2 {
		t.Fatalf("assistant tool calls = %d, want 2", len(asst.ToolCalls))
	}
	if len(tools) != 2 {
		t.Fatalf("tool messages = %d, want 2", len(tools))
	}
	// Each tool result must reference the matching call id, in order.
	if tools[0].ToolCallID != asst.ToolCalls[0].ID || tools[1].ToolCallID != asst.ToolCalls[1].ID {
		t.Errorf("tool_call_id correlation wrong: calls=[%s,%s] tools=[%s,%s]",
			asst.ToolCalls[0].ID, asst.ToolCalls[1].ID, tools[0].ToolCallID, tools[1].ToolCallID)
	}
	if asst.ToolCalls[0].ID == "" {
		t.Error("synthesised tool call id is empty")
	}
}
