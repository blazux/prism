package agent

// Opt-in first-decision probe: real prompt/catalog and provider adapter, synthetic
// context only. Tools are observed, never executed. Complements full-stack eval.
import (
	"context"
	"encoding/json"
	"os"
	"prism/internal/ollama"
	"prism/internal/openai"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPromptBehaviorProbe(t *testing.T) {
	endpoint := os.Getenv("PRISM_PROBE_URL")
	if endpoint == "" {
		t.Skip("set PRISM_PROBE_URL, PRISM_PROBE_MODEL and PRISM_PROBE_OUTPUT for opt-in live evaluation")
	}
	type result struct {
		Profile    string
		Scenario   string
		Tools      []string
		Response   string
		Passed     bool
		Error      string
		DurationMS int64
	}
	cases := []struct{ name, question, tool, contains string }{
		{"definition", "Explique en une phrase ce qu'est un nombre premier.", "", ""},
		{"rewrite", "Reformule poliment cette phrase, sans autre action : Envoie le rapport demain.", "", ""},
		{"supplied-fact", "Le projet fictif Alpha coûte 12 euros et Beta 8 euros. Quel est le total ? Réponds seulement avec le montant.", "", "20"},
		{"clarification", "Configure mon compte mail. Je ne t'ai encore donné ni fournisseur, ni adresse. Demande-moi d'abord le fournisseur et attends ma réponse avant toute action.", "", "?"},
		{"thanks", "Merci, c'est parfait !", "", ""},
		{"create-note", "Crée une note intitulée Courses avec le contenu : acheter du pain.", "note", ""},
		{"mail-unread", "Combien de mails non lus ai-je ?", "email", ""},
		{"relevant-rag", "D'après la collection orchid-care, à quelle fréquence faut-il arroser les orchidées ?", "rag_search", ""},
	}
	var mu sync.Mutex
	var results []result
	t.Run("profiles", func(t *testing.T) {
		for _, profile := range []string{"guided", "standard", "minimal"} {
			t.Run(profile, func(t *testing.T) {
				t.Parallel()
				for _, c := range cases {
					a := &Agent{executor: &ToolExecutor{}, ollama: openai.NewClient(endpoint, os.Getenv("PRISM_PROBE_KEY")), model: os.Getenv("PRISM_PROBE_MODEL"), sessionID: "prompt-probe", limits: Limits{PromptProfile: profile}, turnThinking: true}
					a.ragCtxFn = func() string {
						return RAGCollectionGuidance + "\n- **orchid-care** — Orchid cultivation and watering (4 docs)\n"
					}
					a.history = []ollama.Message{{Role: "user", Content: c.question}}
					ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
					events := make(chan Event, 100)
					done := make(chan struct{})
					go func() {
						for range events {
						}
						close(done)
					}()
					start := time.Now()
					response, calls, _, err := a.callOllama(ctx, "", events)
					cancel()
					close(events)
					<-done
					r := result{Profile: profile, Scenario: c.name, Response: response, DurationMS: time.Since(start).Milliseconds()}
					for _, call := range calls {
						r.Tools = append(r.Tools, call.Function.Name)
					}
					r.Passed = err == nil && strings.TrimSpace(response) != "" && len(calls) == 0
					if c.tool != "" {
						r.Passed = err == nil && len(calls) == 1 && calls[0].Function.Name == c.tool
					}
					if c.contains != "" {
						r.Passed = r.Passed && strings.Contains(response, c.contains)
					}
					if err != nil {
						r.Error = err.Error()
					}
					mu.Lock()
					results = append(results, r)
					mu.Unlock()
					t.Logf("%s passed=%v tools=%v", c.name, r.Passed, r.Tools)
				}
			})
		}
	})
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("PRISM_PROBE_OUTPUT"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("%s/%s: unexpected first decision (tools=%v, error=%s)", r.Profile, r.Scenario, r.Tools, r.Error)
		}
	}
}
