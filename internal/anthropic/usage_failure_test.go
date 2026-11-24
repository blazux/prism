package anthropic

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"prism/internal/ollama"
)

type failingStreamReader struct{ cancel context.CancelFunc }

func (r failingStreamReader) Read(buffer []byte) (int, error) {
	if r.cancel != nil {
		r.cancel()
		return copy(buffer, "data: {}\n\n"), nil
	}
	return 0, errors.New("fixture stream read failure")
}

func TestInterruptedStreamsKeepKnownUsageWithoutToolCalls(t *testing.T) {
	prefix := sse(
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":70,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":15}}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"write_file"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"fixture.txt\"}"}}`,
	)
	for _, failure := range []string{"provider error", "scanner error", "cancellation", "early EOF"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var tail io.Reader
			switch failure {
			case "provider error":
				tail = strings.NewReader(sse(`{"type":"error","error":{"type":"overloaded_error","message":"Fixture failure"}}`))
			case "scanner error":
				tail = failingStreamReader{}
			case "cancellation":
				tail = failingStreamReader{cancel: cancel}
			case "early EOF":
				tail = strings.NewReader("")
			}
			channel := make(chan ollama.StreamEvent, 5)
			(&Client{}).readStream(ctx, io.MultiReader(strings.NewReader(prefix), tail), "fixture", 100, channel, "1h")
			close(channel)
			count := 0
			for event := range channel {
				count++
				if !event.Done || event.DoneReason != "interrupted" || (failure != "early EOF" && event.Err == nil) {
					t.Fatalf("incomplete stream looked successful: %+v", event)
				}
				if len(event.ToolCalls) != 0 {
					t.Fatal("tool call from an incomplete stream was delivered")
				}
				if event.Usage == nil || event.Usage.CacheWritePricingUnknown {
					t.Fatalf("known tiered usage lost on interruption: %+v", event)
				}
				assertUsageCount(t, "input", event.Usage.InputTokens, int64ptr(100))
				assertUsageCount(t, "5m", event.Usage.CacheWrite5mTokens, int64ptr(5))
				assertUsageCount(t, "1h", event.Usage.CacheWrite1hTokens, int64ptr(15))
			}
			if count != 1 {
				t.Fatalf("got %d terminal events", count)
			}
		})
	}
}
