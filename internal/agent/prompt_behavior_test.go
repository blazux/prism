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
	for _, profile := range []string{"guided", "standard", "minimal"} {
		t.Run(profile, func(t *testing.T) {
			b := &stoppingBackend{capturingBackend: capturingBackend{reply: "Les fondations sont conformes. J'écris le module diagrammes."}}
			a := &Agent{ollama: b, executor: &ToolExecutor{}, sessionID: "fixture", limits: Limits{PromptProfile: profile, MaxIterations: 4}}
			events := make(chan Event, 100)
			a.Chat(t.Context(), "Crée le module diagrammes", nil, events)
			close(events)
			if b.calls != 2 {
				t.Fatalf("expected one bounded reminder, got %d calls", b.calls)
			}
			if len(b.req.Messages) == 0 || b.req.Messages[0].Role != "system" || !strings.Contains(b.req.Messages[0].Content, intentNudgeMsg) {
				t.Fatal("reminder missing from the leading system message")
			}
			for _, m := range b.req.Messages[1:] {
				if m.Role == "system" {
					t.Fatal("provider rejects a system message inside history")
				}
			}
			for _, m := range a.history {
				if strings.Contains(m.Content, intentNudgeMsg) {
					t.Fatal("transient reminder leaked into conversation history")
				}
			}
		})
	}
	if shouldNudgeAnnouncement("<think>I'll build it.</think>Quelle ville ?") {
		t.Fatal("reasoning triggered execution")
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
