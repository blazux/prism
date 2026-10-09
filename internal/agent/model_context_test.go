package agent

import (
	"context"
	"prism/internal/ollama"
	"strings"
	"testing"
)

type metadataContextBackend struct {
	fakeSummarizeBackend
	info ollama.ModelCapabilities
}

func (b *metadataContextBackend) ModelCapabilities(context.Context, string) (ollama.ModelCapabilities, error) {
	return b.info, nil
}

func TestUnknownVisionPreservesSettingAndImages(t *testing.T) {
	for _, blind := range []bool{false, true} {
		a := &Agent{
			ollama:    &metadataContextBackend{info: ollama.ModelCapabilities{ContextWindow: 126976}},
			executor:  &ToolExecutor{chatBlind: blind},
			chatBlind: blind,
			history:   []ollama.Message{{Role: "user", Images: []string{"existing-image"}}},
		}
		a.refreshModelCapabilities(context.Background())
		if a.chatBlind != blind || a.executor.chatBlind != blind || len(a.history[0].Images) != 1 || a.modelContextTokens != 126976 {
			t.Fatal("unknown vision changed existing setting or removed images")
		}
	}
}

func TestLargeModelUsesItsWindowAndModelSwitchTightens(t *testing.T) {
	t.Setenv("LIVE_CONTEXT_CHAR_BUDGET", "")
	a := &Agent{modelContextTokens: 500000, contextOverheadChars: 40000, contextCharsPerToken: 3.5}
	a.history = []ollama.Message{{Role: "user", Content: strings.Repeat("x", 150000)}}
	if a.effectiveHistoryBudget() < 1_000_000 || a.needsProactiveCompaction() {
		t.Fatal("large model still uses the old global cap")
	}
	a.modelContextTokens = 32768
	if a.effectiveHistoryBudget() > 100000 || !a.needsProactiveCompaction() {
		t.Fatal("smaller model did not tighten the budget")
	}
	a.contextCharsPerToken = 1.5
	if a.effectiveHistoryBudget() > 10000 {
		t.Fatal("provider token measurements were ignored")
	}
	t.Setenv("LIVE_CONTEXT_CHAR_BUDGET", "150000")
	a.modelContextTokens = 500000
	if a.effectiveHistoryBudget() != liveContextCharBudget {
		t.Fatal("explicit operator ceiling ignored")
	}
}

func TestToolArgumentsAndImagesTriggerCompaction(t *testing.T) {
	a := &Agent{modelContextTokens: 32768, contextOverheadChars: 1000, contextCharsPerToken: 3.5}
	a.history = []ollama.Message{{Role: "user", Images: []string{"image"}}, {Role: "assistant", ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Arguments: []byte(strings.Repeat("x", 100000))}}}}}
	if !a.needsProactiveCompaction() {
		t.Fatal("large arguments/images are absent from budget")
	}
	// Single unfinished turn cannot be cut even when its payload is large.
	a.ollama = &fakeSummarizeBackend{reply: "summary"}
	a.compactLiveContextIfNeeded(context.Background(), nil)
	if len(a.history) != 2 {
		t.Fatal("unfinished tool turn was split")
	}
}
