package agent

import (
	"context"

	"prism/internal/ollama"
)

// UsageRecorder stores content-free model usage independently of conversation history.
// Subagents can keep their history ephemeral while attributing costs to the parent.
type UsageRecorder interface {
	AddUsage(context.Context, int64, string, string, string, int64, map[string]interface{})
}

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

// One record per chat-loop model request, never per streaming delta. Tool-side
// vision/research/embedding calls are not included in these counters.
type ModelUsage struct {
	TaskID                   string        `json:"taskId,omitempty"`
	Scope                    string        `json:"scope"`
	Model                    string        `json:"model"`
	Profile                  string        `json:"profile"`
	DurationMS               int64         `json:"duration_ms"`
	SystemBytes              int           `json:"system_bytes"`
	ToolBytes                int           `json:"tool_bytes"`
	MessageCount             int           `json:"message_count"`
	HistoryContentBytes      int           `json:"history_content_bytes"`
	HistoryToolResultBytes   int           `json:"history_tool_result_bytes"`
	HistoryToolArgumentBytes int           `json:"history_tool_argument_bytes"`
	HistoryImageBytes        int           `json:"history_image_bytes"`
	Complete                 bool          `json:"complete"`
	Usage                    *ollama.Usage `json:"usage"`
}

// historyFootprint reports sizes only; no prompt, arguments or tool output are logged.
func historyFootprint(messages []ollama.Message) (content, toolResults, toolArguments, images int) {
	for i, m := range messages {
		if i == 0 && m.Role == "system" {
			continue // the assembled system prompt has its own size field
		}
		content += len(m.Content)
		if m.Role == "tool" {
			toolResults += len(m.Content)
		}
		for _, tc := range m.ToolCalls {
			toolArguments += len(tc.Function.Arguments)
		}
		for _, image := range m.Images {
			images += len(image)
		}
	}
	return
}
