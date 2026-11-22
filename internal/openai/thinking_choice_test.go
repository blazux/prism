package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestExplicitThinkingChoiceKeepsCallEffortAndToolHistory(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name       string
		choice     *bool
		noThinking bool
		effort     string
		wantChoice *bool
		wantEffort string
	}{
		{"phone low and enabled", &on, false, "low", &on, "low"},
		{"summary none and disabled", &off, true, "", &off, ""},
		{"disabled wins over low", &off, false, "low", &off, ""},
		{"no-thinking wins over enabled", &on, true, "low", &off, ""},
		{"unexpressed boolean stays absent", nil, false, "low", nil, "low"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				var received map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture result.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				c := NewClient(server.URL, "fixture-key")
				out := make(chan ollama.StreamEvent, 16)
				go func() {
					c.Chat(context.Background(), ollama.ChatRequest{Model: "fixture", Stream: stream, EnableThinking: tc.choice, NoThinking: tc.noThinking, ReasoningEffort: tc.effort,
						Messages: []ollama.Message{{Role: "user", Content: "Look up the opening time"},
							{Role: "assistant", ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "search", Arguments: json.RawMessage(`{"query":"opening time"}`)}}}},
							{Role: "tool", Content: "The fixture opens at 8."}},
						Tools: []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{Name: "search"}}}}, out)
					close(out)
				}()
				content, _, _, err := drain(out)
				if err != nil || content != "Fixture result." {
					t.Fatalf("response %q: %v", content, err)
				}
				kwargs, has := received["chat_template_kwargs"].(map[string]any)
				if tc.wantChoice == nil {
					if has {
						t.Fatal("ordinary request gained an explicit thinking choice")
					}
				} else if !has || kwargs["enable_thinking"] != *tc.wantChoice {
					t.Fatalf("thinking kwargs = %#v", kwargs)
				}
				if got, _ := received["reasoning_effort"].(string); got != tc.wantEffort {
					t.Fatalf("reasoning effort = %q", got)
				}
				messages := received["messages"].([]any)
				assistant := messages[1].(map[string]any)
				call := assistant["tool_calls"].([]any)[0].(map[string]any)
				result := messages[2].(map[string]any)
				if call["id"] == "" || result["tool_call_id"] != call["id"] || result["content"] != "The fixture opens at 8." {
					t.Fatal("tool history changed while preserving thinking")
				}
			})
		}
	}
}

func TestNativeOpenAIDoesNotReceiveCompatibleThinkingExtension(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		choice *bool
		effort string
	}{{&on, "low"}, {&off, ""}} {
		c := NewClient("https://api.openai.com/v1", "synthetic-key")
		var received map[string]any
		c.httpClient.Transport = streamFixtureTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "api.openai.com" {
				t.Error("unexpected official endpoint")
			}
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			if _, ok := received["chat_template_kwargs"]; ok {
				t.Error("native API received a compatible-only extension")
			}
			if _, ok := received["top_k"]; ok {
				t.Error("native API received top_k")
			}
			if _, ok := received["max_tokens"]; ok {
				t.Error("explicit native inference used the legacy generation field")
			}
			if received["max_completion_tokens"] != float64(512) {
				t.Error("native generation budget was lost")
			}
			if tc.effort != "" {
				if _, ok := received["temperature"]; ok {
					t.Error("reasoning native API received temperature")
				}
				if _, ok := received["top_p"]; ok {
					t.Error("reasoning native API received top_p")
				}
			} else if received["temperature"] != 0.7 || received["top_p"] != 0.9 {
				t.Error("request without active effort lost sampling settings")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
		})
		k := 20
		p := 0.9
		out := make(chan ollama.StreamEvent, 16)
		go func() {
			c.Chat(context.Background(), ollama.ChatRequest{Model: "fixture", EnableThinking: tc.choice, ReasoningEffort: tc.effort, Options: ollama.Options{Temperature: 0.7, NumPredict: 512, TopP: &p, TopK: &k}}, out)
			close(out)
		}()
		if _, _, _, err := drain(out); err != nil {
			t.Fatal(err)
		}
		if got, _ := received["reasoning_effort"].(string); got != tc.effort {
			t.Fatalf("effort %q, want %q", got, tc.effort)
		}
	}
}
