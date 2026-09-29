package agent

import (
	"context"

	"prism/internal/ollama"
)

type taskIDKey struct{}

// A child agent keeps its parent's task ID through the inherited context.
func withTaskID(ctx context.Context, id string) context.Context {
	if existing, _ := ctx.Value(taskIDKey{}).(string); existing != "" {
		return ctx
	}
	return context.WithValue(ctx, taskIDKey{}, id)
}

func taskIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(taskIDKey{}).(string)
	return id
}

// One record per main-chat model request, never per streaming delta. Tool-side
// vision/research/embedding calls are not included in these chat-loop counters.
type ModelUsage struct {
	TaskID       string        `json:"taskId,omitempty"`
	Scope        string        `json:"scope"`
	Model        string        `json:"model"`
	Profile      string        `json:"profile"`
	DurationMS   int64         `json:"duration_ms"`
	SystemBytes  int           `json:"system_bytes"`
	ToolBytes    int           `json:"tool_bytes"`
	MessageCount int           `json:"message_count"`
	Complete     bool          `json:"complete"`
	Usage        *ollama.Usage `json:"usage"`
}
