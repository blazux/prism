package ollama

// Provider-reported counts. Nil means unavailable, never estimated zero.
// Cached input is a subset of InputTokens; reasoning is a subset of OutputTokens.
// Anthropic's uncached input + cache reads + cache writes are normalized to total input.
type Usage struct {
	InputTokens      *int64 `json:"input_tokens,omitempty"`
	OutputTokens     *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  *int64 `json:"reasoning_tokens,omitempty"`
}
