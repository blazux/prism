package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"prism/internal/ollama"
	"testing"
)

func TestStreamUsageAndCompatibility(t *testing.T) {
	for _, reject := range []bool{false, true} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var req chatRequest
			json.NewDecoder(r.Body).Decode(&req)
			if calls == 1 && !req.StreamOptions["include_usage"] {
				t.Error("usage not requested")
			}
			if reject && req.StreamOptions != nil {
				http.Error(w, "unsupported stream_options", 400)
				return
			}
			fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
			if !reject {
				// Usage-only final chunk, repeated cumulative snapshot must not be added twice.
				for i := 0; i < 2; i++ {
					fmt.Fprintln(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":70},"completion_tokens_details":{"reasoning_tokens":8}}}`)
				}
			}
			fmt.Fprintln(w, "data: [DONE]")
		}))
		ch := make(chan ollama.StreamEvent, 10)
		NewClient(srv.URL, "").Chat(t.Context(), ollama.ChatRequest{Model: "fixture"}, ch)
		close(ch)
		var usage *ollama.Usage
		count := 0
		for ev := range ch {
			if ev.Err != nil {
				t.Fatal(ev.Err)
			}
			if ev.Usage != nil {
				usage = ev.Usage
				count++
			}
		}
		srv.Close()
		if reject {
			if calls != 2 || usage != nil {
				t.Fatal("unsupported usage fallback failed")
			}
		} else if count != 1 || *usage.InputTokens != 100 || *usage.OutputTokens != 20 || *usage.CacheReadTokens != 70 || *usage.ReasoningTokens != 8 {
			t.Fatalf("wrong usage: %+v", usage)
		}
	}
}
