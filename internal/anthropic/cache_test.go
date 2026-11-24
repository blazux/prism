package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"prism/internal/ollama"
)

func cacheFixtureTools() []ollama.Tool {
	var tools []ollama.Tool
	for _, name := range []string{"exec_command", "read_file", "write_file", "widget", "cron", "note", "calendar", "email", "custom_lookup"} {
		tools = append(tools, ollama.Tool{Function: ollama.ToolFunction{Name: name, Description: "Fixture " + name}})
	}
	return tools
}

// Inspect the real HTTP request, including all nested breakpoint locations.
func TestChatCacheTTLAndBreakpointBudget(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, ttl := range []string{"", "5m", "1h", "invalid"} {
			t.Run(fmt.Sprintf("native=%v/ttl=%s", native, ttl), func(t *testing.T) {
				t.Setenv("PRISM_ANTHROPIC_CACHE_TTL", ttl)
				t.Setenv("PRISM_ANTHROPIC_TOOL_SEARCH", "")
				t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "")
				if native {
					t.Setenv("PRISM_ANTHROPIC_TOOL_SEARCH", "1")
				}
				var wire map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if err := json.Unmarshal(body, &wire); err != nil {
						t.Error(err)
					}
					if native {
						fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`)
					} else {
						fmt.Fprint(w, sse(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`))
					}
				}))
				defer server.Close()
				stable := "Stable instructions."
				_, _, _, err := collect(t, NewClient(server.URL, "sk-ant-api03-fixture", ""), ollama.ChatRequest{
					Model: "claude-sonnet-4-6", Messages: []ollama.Message{{Role: "system", Content: stable + " Dynamic context."}, {Role: "user", Content: "Hello"}},
					Tools: cacheFixtureTools(), CacheSystemPrefixBytes: len(stable), CacheToolPrefixCount: 5,
				})
				if err != nil {
					t.Fatal(err)
				}
				breakpoints := 0
				var visit func(any)
				visit = func(value any) {
					switch node := value.(type) {
					case map[string]any:
						if control, ok := node["cache_control"].(map[string]any); ok {
							breakpoints++
							if control["type"] != "ephemeral" {
								t.Fatalf("unexpected cache control: %#v", control)
							}
							if ttl == "1h" {
								if control["ttl"] != "1h" {
									t.Fatalf("cache lifetime differs across breakpoints: %#v", control)
								}
							} else if _, exists := control["ttl"]; exists {
								t.Fatal("default five-minute wire changed")
							}
						}
						if node["defer_loading"] == true && node["cache_control"] != nil {
							t.Fatal("deferred tool must not carry a cache breakpoint")
						}
						for _, child := range node {
							visit(child)
						}
					case []any:
						for _, child := range node {
							visit(child)
						}
					}
				}
				visit(wire)
				want := 4 // native catalog, complete catalog, stable system, automatic history
				if native {
					want = 3 // visible tools, stable system, automatic history
				}
				if breakpoints != want || breakpoints > 4 {
					t.Fatalf("cache breakpoint count=%d, want %d (maximum 4)", breakpoints, want)
				}
			})
		}
	}
}
