package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestClientToolCatalogRequiresOfficialEndpointAndOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, flag, model string
		want                        bool
	}{
		{"default off", "https://api.openai.com/v1", "", "gpt-5.5", false},
		{"flag is explicit", "https://api.openai.com/v1", "true", "gpt-5.5", false},
		{"official", "https://api.openai.com/v1", "1", "gpt-5.5", true},
		{"official normalized trailing slash", "https://api.openai.com/v1/", "1", "gpt-4.1-mini", true},
		{"explicit TLS port", "https://api.openai.com:443/v1", "1", "gpt-4.1-mini", true},
		{"case-insensitive hostname", "https://API.OPENAI.COM/v1", "1", "gpt-4.1-mini", true},
		{"compatible local", "http://127.0.0.1:30000/v1", "1", "gpt-5.5", false},
		{"compatible TLS", "https://gateway.example/v1", "1", "gpt-5.5", false},
		{"HTTP", "http://api.openai.com/v1", "1", "gpt-5.5", false},
		{"different port", "https://api.openai.com:8443/v1", "1", "gpt-5.5", false},
		{"different path", "https://api.openai.com/gateway/v1", "1", "gpt-5.5", false},
		{"missing path", "https://api.openai.com", "1", "gpt-5.5", false},
		{"encoded path", "https://api.openai.com/%761", "1", "gpt-5.5", false},
		{"suffix host", "https://api.openai.com.example/v1", "1", "gpt-5.5", false},
		{"userinfo", "https://user@api.openai.com/v1", "1", "gpt-5.5", false},
		{"query", "https://api.openai.com/v1?route=local", "1", "gpt-5.5", false},
		{"empty query", "https://api.openai.com/v1?", "1", "gpt-5.5", false},
		{"fragment", "https://api.openai.com/v1#route", "1", "gpt-5.5", false},
		{"unknown model", "https://api.openai.com/v1", "1", "gpt-99", false},
		{"qualified selector not resolved", "https://api.openai.com/v1", "1", "paid::gpt-5.5", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PRISM_OPENAI_LAZY_TOOLS", tc.flag)
			c := NewClient(tc.endpoint, "synthetic-key")
			c.httpClient.Transport = streamFixtureTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("catalog policy must not probe models or generate a response")
				return nil, nil
			})
			if got := c.UseClientToolCatalog(t.Context(), tc.model); got != tc.want {
				t.Fatalf("catalog policy=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestClientToolCatalogModelAllowlist(t *testing.T) {
	for _, model := range []string{
		"gpt-4o", "gpt-4o-mini", "gpt-4o-2024-08-06", "gpt-4o-mini-2024-07-18",
		"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4.1-2025-04-14",
		"gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-5-2025-08-07",
		"gpt-5.1", "gpt-5.2", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.5",
	} {
		if !supportsClientToolCatalog(model) {
			t.Errorf("supported function-tool model excluded: %s", model)
		}
	}
	for _, model := range []string{
		"", "gpt-5.6", "gpt-6", "gpt-5.5-pro", "gpt-5.5-chat-latest", "gpt-5-codex",
		"gpt-4o-realtime-preview", "gpt-4o-audio-preview", "gpt-3.5-turbo-instruct", "o3", "o4-mini",
		"gpt-4.1-local", "gpt-5.5-2026-13-01", "gpt-5.5-2026-02-30", "gpt-5.5-2026-11-23-extra",
		"source::gpt-5.5", "claude-sonnet-5-5",
	} {
		if supportsClientToolCatalog(model) {
			t.Errorf("unknown or separate model admitted: %s", model)
		}
	}
}

func TestClientToolCatalogWirePreservesSelectedSchemas(t *testing.T) {
	t.Setenv("PRISM_OPENAI_LAZY_TOOLS", "1")
	c := NewClient("https://api.openai.com/v1", "synthetic-key")
	tools := []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{
		Name: "tool_catalog", Description: "Load permitted tools.",
		Parameters: ollama.ToolParameters{Type: "object", Properties: map[string]ollama.ToolProperty{
			"action": {Type: "string", Enum: []string{"list", "load"}},
			"names":  {Type: "array", Items: &ollama.ToolProperty{Type: "string"}},
		}, Required: []string{"action"}},
	}}}
	c.httpClient.Transport = streamFixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.openai.com/v1/chat/completions" {
			t.Errorf("unexpected destination %s", r.URL)
		}
		var wire struct {
			Tools []chatTool `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		want, _ := json.Marshal(tools[0].Function)
		if len(wire.Tools) != 1 {
			t.Error("provider adapter expanded the agent's selected catalog")
		} else {
			got, _ := json.Marshal(wire.Tools[0].Function)
			if string(got) != string(want) {
				t.Error("catalog schema changed on the wire")
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
	})
	out := make(chan ollama.StreamEvent, 16)
	go func() {
		c.Chat(t.Context(), ollama.ChatRequest{Model: "gpt-5.5", ClientToolCatalog: true, Tools: tools,
			Messages: []ollama.Message{{Role: "user", Content: "Fixture"}}}, out)
		close(out)
	}()
	if content, _, _, err := drain(out); err != nil || content != "ok" {
		t.Fatalf("fake provider response %q: %v", content, err)
	}
}
