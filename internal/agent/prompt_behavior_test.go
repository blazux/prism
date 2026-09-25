package agent

import (
	"context"
	"prism/internal/ollama"
	"strings"
	"testing"
)

type stoppingBackend struct {
	capturingBackend
	calls int
}

func (b *stoppingBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	b.capturingBackend.Chat(ctx, req, out)
}

func TestClarificationEndsActualAgentLoop(t *testing.T) {
	replies := []string{
		"Je vais créer le widget. Quelle ville veux-tu afficher ?",
		"I'll configure it. Which account should I use?",
		"Je vais le faire une fois que tu m'auras indiqué le compte.",
		"I will do that after you confirm the account.",
		"Let me know which account to use.",
		"Je vais supprimer les fichiers. Lesquels veux-tu retirer ?",
	}
	for _, profile := range []string{"guided", "standard", "minimal"} {
		for _, reply := range replies {
			t.Run(profile+"/"+reply, func(t *testing.T) {
				b := &stoppingBackend{capturingBackend: capturingBackend{reply: reply}}
				a := &Agent{ollama: b, executor: &ToolExecutor{}, sessionID: "fixture", limits: Limits{PromptProfile: profile, MaxIterations: 4}}
				events := make(chan Event, 100)
				a.Chat(t.Context(), "Configure le service", nil, events)
				close(events)
				if b.calls != 1 {
					t.Fatalf("clarification forced %d calls instead of stopping", b.calls)
				}
				for _, m := range a.history {
					if m.Content == intentNudgeMsg {
						t.Fatal("clarification overridden by harness")
					}
				}
			})
		}
	}
}

func TestAnnouncementFallbackProfiles(t *testing.T) {
	for _, p := range []string{"guided", "standard", "minimal"} {
		if got := shouldNudgeAnnouncement(p, "Now I'll create the widget."); got != (p == "guided") {
			t.Fatalf("profile %s: %v", p, got)
		}
	}
	if shouldNudgeAnnouncement("guided", "<think>I'll build it.</think>Quelle ville ?") {
		t.Fatal("reasoning triggered execution")
	}
	b := &stoppingBackend{capturingBackend: capturingBackend{reply: "Now I'll create the widget."}}
	a := &Agent{ollama: b, executor: &ToolExecutor{}, sessionID: "fixture", limits: Limits{PromptProfile: "guided", MaxIterations: 4}}
	events := make(chan Event, 100)
	a.Chat(t.Context(), "Create a widget", nil, events)
	close(events)
	if b.calls != 2 {
		t.Fatalf("expected one bounded reminder, got %d calls", b.calls)
	}
	for _, m := range a.history {
		if m.Content == intentNudgeMsg && m.Role != "system" {
			t.Fatal("reminder impersonates user")
		}
	}
}

func TestPromptDecisionAcrossProfiles(t *testing.T) {
	for _, p := range []string{"guided", "standard", "minimal"} {
		a := &Agent{executor: &ToolExecutor{}, sessionID: "fixture", limits: Limits{PromptProfile: p}}
		prompt := a.buildSystemPrompt(t.Context(), "")
		if !strings.Contains(prompt, systemPromptDecision) {
			t.Fatal("missing decision policy", p)
		}
		for _, bad := range []string{"Everything you assert", "default credentials", "always append ?session="} {
			if strings.Contains(prompt, bad) {
				t.Fatal("contradictory or unsafe instruction", p, bad)
			}
		}
	}
}
