package agent

import (
	"context"
	"encoding/json"
	"prism/internal/ollama"
	"strings"
	"testing"
)

func TestPromptProfilePrecedenceAndMinimal(t *testing.T) {
	on, off := true, false
	a := &Agent{executor: &ToolExecutor{}, limits: Limits{LeanPrompt: &on}}
	if a.promptProfile() != "standard" {
		t.Fatal("legacy lean lost")
	}
	a.limits.PromptProfile = "minimal"
	if a.promptProfile() != "minimal" {
		t.Fatal("explicit profile lost")
	}
	a.limitsOverride.LeanPrompt = &off
	if a.promptProfile() != "guided" {
		t.Fatal("legacy override lost")
	}
	a.limitsOverride.PromptProfile = "minimal"
	if a.promptProfile() != "minimal" {
		t.Fatal("profile override lost")
	}
	a.viewCtxFn = func() string { return "OPEN_EDITOR_REVISION_FIXTURE" }
	a.servicesCtxFn = func() string { return "LARGE_SERVICE_INVENTORY" }
	minimal := a.buildSystemPrompt(t.Context(), "")
	for _, expected := range []string{"OPEN_EDITOR_REVISION_FIXTURE", "agent-runtime", "## Destructive actions", "## Pause before heavy", "prismTool(name,args)"} {
		if !strings.Contains(minimal, expected) {
			t.Fatal("lost context/contract", expected)
		}
	}
	if strings.Contains(minimal, "LARGE_SERVICE_INVENTORY") {
		t.Fatal("unconditional inventory")
	}
	a.limitsOverride.PromptProfile = "standard"
	standard := a.buildSystemPrompt(t.Context(), "")
	if len(minimal) >= len(standard)/2 {
		t.Fatalf("minimal too large: %d vs %d", len(minimal), len(standard))
	}
	if !strings.Contains(RuntimeHelp(), "prismOnData") {
		t.Fatal("runtime documentation incomplete")
	}
}

type usageBackend struct{ capturingBackend }

func (b *usageBackend) Chat(_ context.Context, req ollama.ChatRequest, ch chan<- ollama.StreamEvent) {
	b.req = req
	input, output := int64(123), int64(9)
	ch <- ollama.StreamEvent{Content: "ok"}
	ch <- ollama.StreamEvent{Usage: &ollama.Usage{InputTokens: &input, OutputTokens: &output}}
	ch <- ollama.StreamEvent{Done: true, Usage: &ollama.Usage{InputTokens: &input, OutputTokens: &output}}
}
func TestMainChatUsageSingleSnapshot(t *testing.T) {
	a := &Agent{ollama: &usageBackend{}, executor: &ToolExecutor{}, limits: Limits{PromptProfile: "minimal"}, model: "fixture"}
	events := make(chan Event, 20)
	if _, _, _, err := a.callOllama(t.Context(), "", events); err != nil {
		t.Fatal(err)
	}
	close(events)
	count := 0
	for e := range events {
		if e.Type == "model_usage" {
			count++
			if e.Usage == nil || e.Usage.Profile != "minimal" || *e.Usage.Usage.InputTokens != 123 || !e.Usage.Complete {
				t.Fatalf("bad record: %+v", e.Usage)
			}
			raw, _ := json.Marshal(e)
			if strings.Contains(string(raw), "Prism contracts") {
				t.Fatal("telemetry contains prompt")
			}
		}
	}
	if count != 1 {
		t.Fatalf("%d records for one request", count)
	}
}
