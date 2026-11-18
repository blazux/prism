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

func TestGenerationLimitUsesMatchingContextReserve(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		context, limit, want int
	}{
		{"large context with override", 500000, 65536, 65536},
		{"large context automatic", 500000, 0, 16384},
		{"small context retains input room", 32768, 65536, 8192},
		{"unknown context explicit", 0, 65536, 65536},
		{"unknown context automatic", 0, 0, 16384},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &capturingBackend{reply: "ok"}
			a := &Agent{
				ollama: backend, executor: &ToolExecutor{}, model: "fixture", sessionID: "fixture",
				modelContextTokens: tc.context, modelMaxOutputTokens: tc.limit, contextCharsPerToken: 3.5,
				history: []ollama.Message{{Role: "user", Content: "Hello"}},
			}
			if _, _, _, _, err := a.callOllama(t.Context(), "", make(chan Event, 20)); err != nil {
				t.Fatal(err)
			}
			if got := backend.req.Options.NumPredict; got != tc.want {
				t.Fatalf("generation limit sent to backend = %d, want %d", got, tc.want)
			}
			if tc.context > 0 {
				wantInput := int(float64(tc.context-tc.want)*3.5) - a.contextOverheadChars
				if got := a.modelInputBudgetChars(); got != wantInput {
					t.Fatalf("input budget = %d, want %d with the same output reserve", got, wantInput)
				}
			}
			if len(a.history) != 1 || a.history[0].Content != "Hello" {
				t.Fatal("generation setting changed conversation history")
			}
		})
	}
}

func TestModelSwitchDoesNotReuseGenerationLimit(t *testing.T) {
	backend := &metadataContextBackend{info: ollama.ModelCapabilities{ContextWindow: 500000, MaxOutputTokens: 65536}}
	a := &Agent{ollama: backend, executor: &ToolExecutor{}}
	a.refreshModelCapabilities(t.Context())
	if a.modelMaxOutputTokens != 65536 {
		t.Fatal("per-model generation limit not loaded")
	}
	a.SetBackend(&capturingBackend{}, "other")
	if a.modelContextTokens != 0 || a.modelMaxOutputTokens != 0 {
		t.Fatal("new source/model inherited the previous generation limit")
	}
	backend.info = ollama.ModelCapabilities{ContextWindow: 32768}
	a.SetBackend(backend, "small")
	a.refreshModelCapabilities(t.Context())
	if a.modelMaxOutputTokens != 0 || modelOutputReserve(a.modelContextTokens, a.modelMaxOutputTokens) != 8192 {
		t.Fatal("automatic limit not restored for the new model")
	}
}
