package agent

import "prism/internal/ollama"

// One record per main-chat model request, never per streaming delta. Tool-side
// vision/research/embedding calls are not included in these chat-loop counters.
type ModelUsage struct {
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
