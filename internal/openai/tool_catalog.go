package openai

import (
	"context"
	"os"
	"strings"
	"time"
)

// UseClientToolCatalog opts in only on the official Chat Completions endpoint.
// A compatible server or source called "openai" does not establish either the
// provider or the model's support for the tool-loading conversation.
func (c *Client) UseClientToolCatalog(_ context.Context, model string) bool {
	if os.Getenv("PRISM_OPENAI_LAZY_TOOLS") != "1" || !c.officialOpenAI() {
		return false
	}
	return supportsClientToolCatalog(model)
}

// Keep unknown models on the ordinary tool path. These GPT families support
// Chat Completions function tools; pro, realtime, audio and other variants are
// separate models and are not inferred from a shared prefix.
// https://developers.openai.com/api/docs/models
func supportsClientToolCatalog(model string) bool {
	for _, family := range []string{
		"gpt-4o", "gpt-4o-mini",
		"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano",
		"gpt-5", "gpt-5-mini", "gpt-5-nano",
		"gpt-5.1", "gpt-5.2",
		"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.5",
	} {
		if model == family {
			return true
		}
		if date, ok := strings.CutPrefix(model, family+"-"); ok && len(date) == len("2006-01-02") {
			if _, err := time.Parse("2006-01-02", date); err == nil {
				return true
			}
		}
	}
	return false
}
