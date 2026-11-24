package anthropic

import (
	"context"
	"os"
	"strings"
	"time"

	"prism/internal/ollama"
)

// UseClientToolCatalog is an optional backend capability. Only the client that
// will actually issue a Messages request opts in; a model-name or source-name
// prefix cannot turn an unrelated compatible endpoint into an Anthropic client.
func (c *Client) UseClientToolCatalog(_ context.Context, _ string) bool {
	return os.Getenv("PRISM_CLAUDE_LAZY_TOOLS") == "1"
}

func useNativeToolSearch(request ollama.ChatRequest) bool {
	return os.Getenv("PRISM_ANTHROPIC_TOOL_SEARCH") == "1" &&
		!request.ClientToolCatalog && len(request.Tools) >= 8 &&
		supportsToolSearch(request.Model)
}

// Anthropic's Models API does not expose a tool-search capability. Keep this
// conservative list tied to the documented Model compatibility table rather
// than assuming every newer version or code-execution-capable model supports it.
// Table checked on 2026-11-23; unknown versions remain on the ordinary tool path.
// https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool
func supportsToolSearch(model string) bool {
	for _, family := range []string{
		"claude-opus-4-5", "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5",
		"claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-5", "claude-sonnet-5-5",
		"claude-haiku-4-5", "claude-haiku-5-5",
		"claude-fable-5", "claude-fable-5-1", "claude-fable-5-2",
		"claude-mythos-5", "claude-mythos-5-1", "claude-mythos-5-2",
	} {
		if model == family {
			return true
		}
		if suffix, ok := strings.CutPrefix(model, family+"-"); ok && len(suffix) == 8 {
			if _, err := time.Parse("20060102", suffix); err == nil {
				return true
			}
		}
	}
	return false
}
