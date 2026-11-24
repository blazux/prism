package openai

import (
	"testing"

	"prism/internal/ollama"
)

func TestStreamErrorPreservesUsageWithoutExecutableTools(t *testing.T) {
	for _, sameEnvelope := range []bool{false, true} {
		lines := []string{`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"write_file","arguments":"{}"}}]}}]}`}
		if sameEnvelope {
			lines = append(lines, `data: {"error":{"message":"fixture error"},"usage":{"prompt_tokens":123,"completion_tokens":7}}`)
		} else {
			lines = append(lines, `data: {"choices":[],"usage":{"prompt_tokens":123,"completion_tokens":7}}`, `data: {"error":{"message":"fixture error"}}`)
		}
		srv := sseServer(t, lines)
		c := NewClient(srv.URL, "")
		ch := make(chan ollama.StreamEvent, 20)
		c.Chat(t.Context(), ollama.ChatRequest{Model: "fixture"}, ch)
		close(ch)
		failed := false
		for event := range ch {
			if len(event.ToolCalls) > 0 {
				t.Fatal("incomplete tool call exposed after provider error")
			}
			if event.Err == nil {
				continue
			}
			failed = true
			if event.Usage == nil || event.Usage.InputTokens == nil || *event.Usage.InputTokens != 123 || event.Usage.OutputTokens == nil || *event.Usage.OutputTokens != 7 || event.DoneReason != "interrupted" || event.Done {
				t.Fatalf("known usage lost or failed call marked complete: %+v", event)
			}
		}
		srv.Close()
		if !failed {
			t.Fatal("provider error was ignored")
		}
	}
}
