package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"prism/internal/ollama"
)

func TestSourceRelayKeepsKnownUsageAfterCancellation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[],"usage":{"prompt_tokens":123,"completion_tokens":7}}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"received"}}]}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	b := &sourcesBackend{primary: "fixture", sources: []aiSource{{ID: "fixture", Provider: "openai", BaseURL: upstream.URL, Model: "fixture"}}}
	// A cancelled select must preserve usage whichever ready case it selects.
	for range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		out := make(chan ollama.StreamEvent, 10)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer close(out)
			b.Chat(ctx, ollama.ChatRequest{Model: "fixture", TaskID: "parent"}, out)
		}()
		select {
		case event := <-out:
			if event.Content != "received" {
				t.Fatalf("provider did not parse the usage preceding content: %+v", event)
			}
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("provider did not start")
		}
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled provider relay did not exit")
		}
		known := false
		for event := range out {
			if len(event.ToolCalls) != 0 {
				t.Fatal("cancelled provider exposed executable tools")
			}
			if event.Usage != nil {
				known = event.Usage.InputTokens != nil && *event.Usage.InputTokens == 123 &&
					event.Usage.OutputTokens != nil && *event.Usage.OutputTokens == 7 && !event.Done
			}
		}
		if !known {
			t.Fatal("source relay discarded known terminal usage after cancellation")
		}
	}
}
