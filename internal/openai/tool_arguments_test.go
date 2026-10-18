package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"prism/internal/ollama"
	"testing"
)

func TestMalformedToolArgumentsAreRejectedWithoutPoisoningHistory(t *testing.T) {
	for _, args := range []string{`{"path":"audit/app.py","content":"unfinished`, `null`, `[]`, `"text"`} {
		t.Run(args, func(t *testing.T) {
			delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]string{"name": "write_file", "arguments": args}}}}}}})
			srv := sseServer(t, []string{"data: " + string(delta), `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, "data: [DONE]"})
			defer srv.Close()
			ch := make(chan ollama.StreamEvent, 20)
			go func() {
				NewClient(srv.URL, "").Chat(context.Background(), ollama.ChatRequest{Model: "fixture"}, ch)
				close(ch)
			}()
			_, _, calls, err := drain(ch)
			if err != nil || len(calls) != 1 || calls[0].Function.ArgumentError == "" {
				t.Fatalf("malformed arguments accepted: %#v, %v", calls, err)
			}
			history := []ollama.Message{{Role: "assistant", ToolCalls: calls}, {Role: "tool", Content: "Error: " + calls[0].Function.ArgumentError}}
			encoded, err := json.Marshal(buildMessages(history))
			if err != nil || !json.Valid(encoded) {
				t.Fatalf("replay poisoned: %s %v", encoded, err)
			}
		})
	}
}

func TestValidEmptyToolArgumentsRemainSupported(t *testing.T) {
	for _, args := range []string{"", "{}"} {
		acc := newToolAccumulator()
		acc.add([]respToolCall{{Index: 0, Function: respToolCallFn{Name: "list_tools", Arguments: args}}})
		calls := acc.result()
		if len(calls) != 1 || calls[0].Function.ArgumentError != "" {
			t.Fatal(fmt.Sprint(calls))
		}
	}
}
