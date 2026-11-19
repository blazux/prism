package agent

import (
	"context"
	"encoding/json"
	"strings"

	"prism/internal/ollama"
)

// modelStreamResult preserves the provider's payload and terminal metadata.
// Callers choose their own acceptance policy: the chat can keep a partial reply,
// whereas compaction must reject anything except a complete text-only summary.
type modelStreamResult struct {
	Content        string
	ToolCalls      []ollama.ToolCall
	ProviderBlocks []json.RawMessage
	Usage          *ollama.Usage
	DoneReason     string
	Done           bool
	Complete       bool
}

func collectModelStream(ctx context.Context, backend ollama.Backend, req ollama.ChatRequest, onEvent func(ollama.StreamEvent)) (result modelStreamResult, err error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan ollama.StreamEvent, 100)
	go func() {
		backend.Chat(streamCtx, req, ch)
		close(ch)
	}()

	var content strings.Builder
	defer func() { result.Content = content.String() }()
	for ev := range ch {
		// Usage can arrive with the terminal chunk after cancellation. Keep its
		// counters while refusing late content and incomplete tool calls.
		if ev.Usage != nil {
			result.Usage = ev.Usage
		}
		if ev.Done {
			result.Done = true
			result.Complete = ev.DoneReason != "interrupted"
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if ev.Err != nil {
			return result, ev.Err
		}
		if ev.DoneReason != "" {
			result.DoneReason = ev.DoneReason
		}
		content.WriteString(ev.Content)
		result.ToolCalls = append(result.ToolCalls, ev.ToolCalls...)
		if len(ev.ProviderBlocks) > 0 {
			result.ProviderBlocks = ev.ProviderBlocks
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return result, ctx.Err()
}
