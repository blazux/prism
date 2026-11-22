package ollama

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExplicitThinkingChoiceUsesSupportedNativeMode(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name       string
		choice     *bool
		effort     string
		noThinking bool
		caps       []string
		levels     []any
		want       any
	}{
		{"boolean phone model", &on, "low", false, []string{"thinking"}, nil, true},
		{"named low phone model", &on, "low", false, []string{"thinking"}, []any{false, "low", "high"}, "low"},
		{"summary disabled", &off, "", true, []string{"thinking"}, []any{false, "low"}, false},
		{"explicit false wins over low", &off, "low", false, []string{"thinking"}, []any{false, "low"}, false},
		{"no-thinking wins over true", &on, "low", true, []string{"thinking"}, nil, false},
		{"non-thinking model stays compatible", &on, "low", false, []string{"completion"}, nil, nil},
		{"ordinary request stays unchanged", nil, "", false, []string{"thinking"}, nil, nil},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				var received map[string]any
				showCalls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/show" {
						showCalls++
						json.NewEncoder(w).Encode(map[string]any{"capabilities": tc.caps, "thinking": map[string]any{"values": tc.levels}})
						return
					}
					if r.URL.Path != "/api/chat" {
						t.Errorf("unexpected route %s", r.URL.Path)
					}
					if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
						t.Error(err)
					}
					json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "Fixture result."}, "done": true, "done_reason": "stop"})
				}))
				defer server.Close()
				drain(NewClient(server.URL), ChatRequest{Model: "fixture", Stream: stream, EnableThinking: tc.choice, NoThinking: tc.noThinking, ReasoningEffort: tc.effort,
					Messages: []Message{{Role: "user", Content: "Fixture question"}}, Options: Options{NumPredict: 512}})
				if received["think"] != tc.want {
					t.Fatalf("think=%#v, want %#v", received["think"], tc.want)
				}
				if tc.choice == nil && showCalls != 0 {
					t.Fatal("ordinary request performed thinking discovery")
				}
				if opts := received["options"].(map[string]any); opts["num_predict"] != float64(512) {
					t.Fatal("thinking choice changed the generation ceiling")
				}
			})
		}
	}
}
