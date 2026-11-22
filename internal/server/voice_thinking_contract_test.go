package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoiceRelayPreservesCallAndSummaryThinkingChoices(t *testing.T) {
	for _, tc := range []struct {
		name     string
		effort   string
		thinking bool
	}{{"phone call", "low", true}, {"call summary", "none", false}} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				var forwarded map[string]any
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := json.NewDecoder(r.Body).Decode(&forwarded); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture result.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer provider.Close()
				s := New(Config{LLMBackend: "openai", OpenAIBaseURL: provider.URL + "/v1", Model: "fixture"})
				s.voxState.Store(&voxConnection{Enabled: true, Model: "default", GatewayToken: "gateway-fixture"})
				body, _ := json.Marshal(map[string]any{"model": "default", "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "Synthetic request"}},
					"reasoning_effort": tc.effort, "chat_template_kwargs": map[string]any{"enable_thinking": tc.thinking}, "max_tokens": 128})
				r := withUser(httptest.NewRequest(http.MethodPost, "/api/voice/inference/v1/chat/completions", bytes.NewReader(body)), serviceUser)
				r.Header.Set("Authorization", "Bearer gateway-fixture")
				w := httptest.NewRecorder()
				s.handleVoiceInference(w, r)
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Fixture result.") {
					t.Fatalf("relay response %d: %s", w.Code, w.Body)
				}
				kwargs, ok := forwarded["chat_template_kwargs"].(map[string]any)
				if !ok || kwargs["enable_thinking"] != tc.thinking {
					t.Fatalf("thinking choice changed: %#v", forwarded)
				}
				if tc.thinking {
					if forwarded["reasoning_effort"] != "low" {
						t.Fatal("call effort was lost")
					}
				} else if _, ok := forwarded["reasoning_effort"]; ok {
					t.Fatal("summary gained a reasoning effort")
				}
				if stream && !strings.Contains(w.Body.String(), "data: [DONE]") {
					t.Fatal("stream missing completion marker")
				}
			})
		}
	}
}
