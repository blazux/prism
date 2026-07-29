package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prism/internal/ollama"
)

// sse renders the events Anthropic streams for one turn.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", eventType(e), e)
	}
	return b.String()
}

func eventType(payload string) string {
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(payload), &probe)
	return probe.Type
}

// collect drains a Chat stream into the text, thinking and tool calls it yielded.
func collect(t *testing.T, c *Client, req ollama.ChatRequest) (string, string, []ollama.ToolCall, error) {
	t.Helper()
	ch := make(chan ollama.StreamEvent, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		c.Chat(ctx, req, ch)
		close(ch)
	}()

	var text, thinking strings.Builder
	var calls []ollama.ToolCall
	var err error
	for ev := range ch {
		if ev.Err != nil {
			err = ev.Err
		}
		text.WriteString(ev.Content)
		thinking.WriteString(ev.Thinking)
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
	}
	return text.String(), thinking.String(), calls, err
}

func TestChatStreamsTextAndToolCalls(t *testing.T) {
	var gotBody []byte
	var gotHeader http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse(
			`{"type":"message_start","message":{"id":"msg_1"}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Reading "}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the file."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_x","name":"mcp__read_file"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"/tmp/x\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
			`{"type":"message_stop"}`,
		))
	}))
	defer srv.Close()

	// A "cc-" token is a subscription credential, so this exercises the OAuth wire.
	c := NewClient(srv.URL, NewTokenSource("cc-test-token", ""), "9.9.9", "claude-test")
	text, _, calls, err := collect(t, c, ollama.ChatRequest{
		Model: "claude-test",
		Messages: []ollama.Message{
			{Role: "system", Content: "You are PRISM."},
			{Role: "user", Content: "read /tmp/x"},
		},
		Tools: []ollama.Tool{{Function: ollama.ToolFunction{Name: "read_file", Description: "read a file"}}},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	if text != "Reading the file." {
		t.Errorf("text deltas did not reassemble, got %q", text)
	}
	if len(calls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("the wire prefix should be stripped, got %q", calls[0].Function.Name)
	}
	if string(calls[0].Function.Arguments) != `{"path":"/tmp/x"}` {
		t.Errorf("tool arguments did not reassemble, got %s", calls[0].Function.Arguments)
	}

	// The subscription fingerprint: without it Anthropic does not route the token.
	if got := gotHeader.Get("Authorization"); got != "Bearer cc-test-token" {
		t.Errorf("expected bearer auth, got %q", got)
	}
	if got := gotHeader.Get("x-api-key"); got != "" {
		t.Errorf("an OAuth request must not also send x-api-key, got %q", got)
	}
	if got := gotHeader.Get("User-Agent"); got != "claude-code/9.9.9 (external, cli)" {
		t.Errorf("unexpected user-agent %q", got)
	}
	if got := gotHeader.Get("x-app"); got != "cli" {
		t.Errorf("expected x-app: cli, got %q", got)
	}
	beta := gotHeader.Get("anthropic-beta")
	for _, want := range oauthBetas {
		if !strings.Contains(beta, want) {
			t.Errorf("anthropic-beta %q is missing %q", beta, want)
		}
	}
	if gotHeader.Get("anthropic-version") == "" {
		t.Error("anthropic-version is required on every request")
	}

	var sent apiRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("request body was not valid JSON: %v", err)
	}
	if len(sent.System) == 0 || sent.System[0].Text != claudeCodeSystemPrefix {
		t.Errorf("the Claude Code identity block must lead the system prompt, got %+v", sent.System)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "mcp__read_file" {
		t.Errorf("tools must go out prefixed, got %+v", sent.Tools)
	}
	if sent.MaxTokens != defaultMaxTokens {
		t.Errorf("max_tokens is mandatory, expected the default %d, got %d", defaultMaxTokens, sent.MaxTokens)
	}
	if !sent.Stream {
		t.Error("expected a streaming request")
	}
}

func TestChatUsesAPIKeyHeaderWithoutTheCLIFingerprint(t *testing.T) {
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		fmt.Fprint(w, sse(`{"type":"message_stop"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, NewTokenSource("sk-ant-api03-key", ""), "", "claude-test")
	if _, _, _, err := collect(t, c, ollama.ChatRequest{
		Model:    "claude-test",
		Messages: []ollama.Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	if got := gotHeader.Get("x-api-key"); got != "sk-ant-api03-key" {
		t.Errorf("a console key authenticates with x-api-key, got %q", got)
	}
	if got := gotHeader.Get("Authorization"); got != "" {
		t.Errorf("a console key must not use bearer auth, got %q", got)
	}
	// Sending Claude Code's identity on an API key would be claiming to be a CLI
	// this request has nothing to do with.
	if got := gotHeader.Get("x-app"); got != "" {
		t.Errorf("api-key requests must not carry the CLI fingerprint, got x-app %q", got)
	}
	if beta := gotHeader.Get("anthropic-beta"); strings.Contains(beta, "oauth-2025-04-20") {
		t.Errorf("api-key requests must not send the OAuth betas, got %q", beta)
	}
}

func TestChatSurfacesStreamErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sse(
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
			`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, NewTokenSource("cc-token", ""), "1.0.0", "claude-test")
	_, _, _, err := collect(t, c, ollama.ChatRequest{
		Model:    "claude-test",
		Messages: []ollama.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("a mid-stream error event must surface as an error")
	}
	if !strings.Contains(err.Error(), "Overloaded") {
		t.Errorf("the upstream message should reach the caller, got %v", err)
	}
}

func TestChatDoesNotRetryPlanLimit(t *testing.T) {
	// 429 on a subscription means the usage window is exhausted; retrying burns
	// the turn's time for nothing and buries Anthropic's explanation.
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"usage limit reached"}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, NewTokenSource("cc-token", ""), "1.0.0", "claude-test")
	_, _, _, err := collect(t, c, ollama.ChatRequest{
		Model:    "claude-test",
		Messages: []ollama.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected the rate-limit error to surface")
	}
	if attempts != 1 {
		t.Errorf("expected exactly one attempt, got %d", attempts)
	}
	if !strings.Contains(err.Error(), "usage limit reached") {
		t.Errorf("Anthropic's own message should reach the caller, got %v", err)
	}
}

func TestChatRetriesOverloaded(t *testing.T) {
	old := retryBaseBackoff
	retryBaseBackoff = time.Millisecond
	defer func() { retryBaseBackoff = old }()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(529) // Anthropic's "overloaded", clears on its own
			return
		}
		fmt.Fprint(w, sse(
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_stop"}`,
		))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, NewTokenSource("cc-token", ""), "1.0.0", "claude-test")
	text, _, _, err := collect(t, c, ollama.ChatRequest{
		Model:    "claude-test",
		Messages: []ollama.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("a transient 529 should be retried, got %v", err)
	}
	if text != "ok" {
		t.Errorf("expected the retried turn's text, got %q", text)
	}
	if attempts != 2 {
		t.Errorf("expected one retry, got %d attempts", attempts)
	}
}

func TestListModelsFallsBackWhenEnumerationIsRefused(t *testing.T) {
	// A subscription token is scoped for inference and may not list models. An
	// empty picker would look like a broken backend.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, NewTokenSource("cc-token", ""), "1.0.0", "claude-configured")
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels should degrade, not fail: %v", err)
	}
	if len(models) == 0 || models[0] != "claude-configured" {
		t.Errorf("the configured model must be offered first, got %v", models)
	}
}

func TestPingRejectsABadCredentialButToleratesAScopedOne(t *testing.T) {
	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, NewTokenSource("cc-token", ""), "1.0.0", "claude-test")
	if err := c.Ping(context.Background()); err == nil {
		t.Error("a 401 means the token is dead and Ping must say so")
	}

	// 403 says the token authenticates but cannot enumerate models, which tells
	// us nothing about inference.
	status = http.StatusForbidden
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("a scoped token should still report healthy, got %v", err)
	}
}

func TestTokenSourceReportsMissingCredentialsClearly(t *testing.T) {
	ts := NewTokenSource("", "/nonexistent/.credentials.json")
	_, err := ts.Token(context.Background())
	if err == nil {
		t.Fatal("expected an error when there are no credentials at all")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("the error should point at how to log in, got %v", err)
	}
}

func TestStreamErrorExplainsARefusalDisguisedAsOverload(t *testing.T) {
	// With tools on a subscription token, "Overloaded" is Anthropic refusing
	// third-party tool use for that model, not a busy server. Reporting it
	// verbatim sends the reader off waiting for capacity that was never the
	// problem.
	err := explainStreamError("overloaded_error", "Overloaded", true, true, "claude-sonnet-5")
	for _, want := range []string{"claude-sonnet-5", "extra usage", "retrying may work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the explanation should mention %q, got %v", want, err)
		}
	}

	// Without tools, or on an API key, an overload really is an overload.
	plain := explainStreamError("overloaded_error", "Overloaded", true, false, "claude-sonnet-5")
	if strings.Contains(plain.Error(), "extra usage") {
		t.Errorf("a tool-less turn should report the error as-is, got %v", plain)
	}
	onKey := explainStreamError("overloaded_error", "Overloaded", false, true, "claude-sonnet-5")
	if strings.Contains(onKey.Error(), "extra usage") {
		t.Errorf("an api-key turn should report the error as-is, got %v", onKey)
	}
	other := explainStreamError("api_error", "boom", true, true, "claude-sonnet-5")
	if !strings.Contains(other.Error(), "boom") {
		t.Errorf("other error types must pass through, got %v", other)
	}
}
