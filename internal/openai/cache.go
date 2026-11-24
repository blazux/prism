package openai

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"unicode/utf8"

	"prism/internal/ollama"
)

// Earlier OpenAI models use a routing key; GPT-5.6 and later route automatically.
// New prompt_cache_options/breakpoints belong to Responses, not this adapter.
// https://developers.openai.com/api/docs/guides/prompt-caching
func (c *Client) configurePromptCache(payload *chatRequest, req ollama.ChatRequest) {
	if !c.officialOpenAI() || nativePolicy(req.Model).automaticRouting {
		return
	}
	if len(nativePolicy(req.Model).efforts) == 0 && !supportsExtendedCacheRetention(req.Model) && !supportsClientToolCatalog(req.Model) {
		return // unknown model contracts keep the provider's automatic defaults
	}
	// Only the explicitly marked shared system prefix is suitable. Falling back
	// to the whole system prompt would include dynamic workspace/session data and
	// produce a different routing key on every conversation. Automatic caching
	// still works when a caller supplies no shared-prefix marker.
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
		prefixLen := req.CacheSystemPrefixBytes
		text := req.Messages[0].Content
		if prefixLen > 0 && prefixLen <= len(text) && utf8.ValidString(text[:prefixLen]) {
			mac := hmac.New(sha256.New, []byte(c.apiKey))
			mac.Write([]byte("prism-openai-cache-v1\x00" + req.Model + "\x00"))
			mac.Write([]byte(text[:prefixLen]))
			// A keyed digest keeps credentials, persona text and other private
			// instructions out of the public routing field. It is never logged.
			payload.PromptCacheKey = "prism-v1-" + hex.EncodeToString(mac.Sum(nil))[:48]
		}
	}
	if strings.TrimSpace(os.Getenv("PRISM_OPENAI_CACHE_RETENTION")) == "24h" && supportsExtendedCacheRetention(req.Model) {
		payload.PromptCacheRetention = "24h"
	}
}

// Exact model IDs from OpenAI's extended-retention list and documented snapshots.
// Do not infer support for mini/pro/chat/future variants from a family prefix.
// Omission keeps the provider/organization default, including ZDR policy.
func supportsExtendedCacheRetention(model string) bool {
	switch model {
	case "gpt-5.5", "gpt-5.5-2026-04-23", "gpt-5.5-pro",
		"gpt-5.4", "gpt-5.4-2026-03-05",
		"gpt-5.2", "gpt-5.2-2025-12-11",
		"gpt-5.1", "gpt-5.1-2025-11-13", "gpt-5.1-codex-max", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-chat-latest",
		"gpt-5", "gpt-5-2025-08-07", "gpt-5-codex",
		"gpt-4.1", "gpt-4.1-2025-04-14":
		return true
	default:
		return false
	}
}
