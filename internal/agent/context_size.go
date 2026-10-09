package agent

import "prism/internal/ollama"

// Byte-equivalent footprint calibrated from provider input usage. Image bytes
// are base64, not tokens: reserve a conservative per-image allowance instead.
func messageContextChars(m ollama.Message) int {
	n := len(m.Content) + len(m.Thinking) + 24
	for _, tc := range m.ToolCalls {
		n += len(tc.Function.Name) + len(tc.Function.Arguments) + 32
	}
	for _, block := range m.ProviderBlocks {
		n += len(block)
	}
	n += len(m.Images) * 8192 * 4
	return n
}
