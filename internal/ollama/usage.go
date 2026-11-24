package ollama

// Provider-reported counts. Nil means unavailable, never estimated zero.
// Cached input is a subset of InputTokens; reasoning is a subset of OutputTokens.
// Anthropic's uncached input + cache reads + cache writes are normalized to total input.
type Usage struct {
	InputTokens        *int64 `json:"input_tokens,omitempty"`
	OutputTokens       *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens    *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens   *int64 `json:"cache_write_tokens,omitempty"`
	CacheWrite5mTokens *int64 `json:"cache_write_5m_tokens,omitempty"`
	CacheWrite1hTokens *int64 `json:"cache_write_1h_tokens,omitempty"`
	// Do not price unknown write tiers as if they were all five-minute writes.
	CacheWritePricingUnknown bool   `json:"cache_write_pricing_unknown,omitempty"`
	ReasoningTokens          *int64 `json:"reasoning_tokens,omitempty"`
	// Only an adapter connected to a verified billing endpoint sets provenance.
	// A compatible model name alone does not identify its provider or tariff.
	PricingProvider string `json:"pricing_provider,omitempty"`
	ServiceTier     string `json:"service_tier,omitempty"`
}
