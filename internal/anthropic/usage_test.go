package anthropic

import (
	"prism/internal/ollama"
	"strings"
	"testing"
)

func TestStreamUsageSnapshots(t *testing.T) {
	for _, stop := range []bool{false, true} {
		raw := sse(`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":70,"cache_creation_input_tokens":20}}}`, `{"type":"message_delta","usage":{"output_tokens":8}}`, `{"type":"message_delta","usage":{"output_tokens":12},"delta":{"stop_reason":"end_turn"}}`)
		if stop {
			raw += sse(`{"type":"message_stop"}`)
		}
		ch := make(chan ollama.StreamEvent, 10)
		(&Client{}).readStream(t.Context(), strings.NewReader(raw), "fixture", 100, ch)
		close(ch)
		count := 0
		for ev := range ch {
			if ev.Usage != nil {
				count++
				if *ev.Usage.InputTokens != 100 || *ev.Usage.OutputTokens != 12 || *ev.Usage.CacheReadTokens != 70 || *ev.Usage.CacheWriteTokens != 20 {
					t.Fatalf("wrong usage: %+v", ev.Usage)
				}
				if !stop && ev.DoneReason != "interrupted" {
					t.Fatal("incomplete stream reported complete")
				}
			}
		}
		if count != 1 {
			t.Fatalf("%d usage events", count)
		}
	}
}
