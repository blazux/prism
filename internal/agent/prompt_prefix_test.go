package agent

import (
	"fmt"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestPromptPrefixCacheTracksSourceValues(t *testing.T) {
	for _, profile := range []string{"guided", "standard", "minimal"} {
		t.Run(profile, func(t *testing.T) {
			a := &Agent{executor: &ToolExecutor{}, agentName: "Alpha", sessionID: "u1-first", basePersonality: "Base A", personality: "Adaptation A", limits: Limits{PromptProfile: profile}}
			previous, prefix := a.buildSystemPromptWithCachePrefix(t.Context(), "")
			if again, againPrefix := a.buildSystemPromptWithCachePrefix(t.Context(), ""); previous != again || prefix != againPrefix {
				t.Fatal("identical inputs changed the prompt or cache boundary")
			}
			for _, change := range []struct {
				apply func()
				want  string
			}{
				{func() { a.agentName = "Beta" }, "Your name is Beta."},
				{func() { a.basePersonality = "Base B" }, "Base B"},
				{func() { a.personality = "Adaptation B" }, "Adaptation B"},
				{func() {
					a.limitsOverride.PromptProfile = "minimal"
					if profile == "minimal" {
						a.limitsOverride.PromptProfile = "guided"
					}
				}, ""},
			} {
				change.apply()
				current, prefix := a.buildSystemPromptWithCachePrefix(t.Context(), "")
				if current == previous || !strings.Contains(current[:prefix], change.want) || a.promptCache != current[:prefix] {
					t.Fatal("changed source value reused an obsolete instruction prefix")
				}
				previous = current
			}
		})
	}
}

func TestPromptPrefixIsSharedAcrossSessions(t *testing.T) {
	for _, profile := range []string{"guided", "standard", "minimal"} {
		t.Run(profile, func(t *testing.T) {
			makeAgent := func(session string) *Agent {
				return &Agent{executor: &ToolExecutor{}, agentName: "Alpha", sessionID: session,
					basePersonality: "Base persona", personality: "Workspace voice", limits: Limits{PromptProfile: profile}}
			}
			firstAgent, secondAgent := makeAgent("u1-cache-board-alpha"), makeAgent("u1-cache-board-beta")
			first, firstPrefix := firstAgent.buildSystemPromptWithCachePrefix(t.Context(), "")
			second, secondPrefix := secondAgent.buildSystemPromptWithCachePrefix(t.Context(), "")
			if firstPrefix == 0 || firstPrefix != secondPrefix || first[:firstPrefix] != second[:secondPrefix] {
				t.Fatal("identical names, personalities and profiles did not share an instruction prefix")
			}
			for _, tc := range []struct {
				prompt, session, other string
			}{
				{first, firstAgent.sessionID, secondAgent.sessionID},
				{second, secondAgent.sessionID, firstAgent.sessionID},
			} {
				if strings.Contains(tc.prompt[:firstPrefix], tc.session) ||
					strings.Count(tc.prompt, tc.session) != 1 || strings.Contains(tc.prompt, tc.other) {
					t.Fatal("session identity was cached, duplicated or leaked between sessions")
				}
				if !strings.Contains(tc.prompt[firstPrefix:], "Current session ID: `"+tc.session+"`") ||
					!strings.Contains(tc.prompt[firstPrefix:], "Wherever these instructions say SESSION_ID, use this exact string.") {
					t.Fatal("dynamic suffix lost the session identity or SESSION_ID substitution rule")
				}
			}
			firstAgent.sessionID = secondAgent.sessionID
			changed, changedPrefix := firstAgent.buildSystemPromptWithCachePrefix(t.Context(), "")
			if changedPrefix != firstPrefix || changed[:changedPrefix] != first[:firstPrefix] || changed != second {
				t.Fatal("changing sessions on an existing agent changed the instruction prefix or kept a stale session")
			}
		})
	}
}

func TestPromptPrefixCacheKeepsAllContextHooksLive(t *testing.T) {
	a := &Agent{executor: &ToolExecutor{}, sessionID: "fixture"}
	version := 1
	calls := map[string]int{}
	hook := func(name string) func() string {
		return func() string { calls[name]++; return fmt.Sprintf("%s-v%d", name, version) }
	}
	a.userProfileFn, a.ragCtxFn, a.mcpCtxFn = hook("profile"), hook("rag"), hook("mcp")
	a.skillsCtxFn, a.servicesCtxFn, a.viewCtxFn, a.globalCtxFn = hook("skills"), hook("services"), hook("view"), hook("global")
	first, prefix := a.buildSystemPromptWithCachePrefix(t.Context(), "lesson-v1")
	version++
	second, secondPrefix := a.buildSystemPromptWithCachePrefix(t.Context(), "lesson-v2")
	if prefix != secondPrefix || first[:prefix] != second[:prefix] {
		t.Fatal("live context altered the instruction prefix")
	}
	for name, count := range calls {
		if count != 2 || !strings.Contains(second[prefix:], name+"-v2") || strings.Contains(second[prefix:], name+"-v1") {
			t.Fatalf("%s was cached or omitted: calls=%d", name, count)
		}
	}
	if !strings.Contains(second[prefix:], "lesson-v2") || strings.Contains(second[prefix:], "lesson-v1") {
		t.Fatal("lessons became stale")
	}
}

func BenchmarkSystemPromptPrefix(b *testing.B) {
	key := systemPromptPrefixKey{name: "Prism", persona: "A helpful generalist", profile: "guided"}
	b.Run("assemble", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = buildSystemPromptPrefix(key)
		}
	})
	b.Run("reuse", func(b *testing.B) {
		a := &Agent{}
		_ = a.cachedSystemPromptPrefix(key)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_ = a.cachedSystemPromptPrefix(key)
		}
	})
}

func TestPromptPrefixCacheKeepsToolCatalogLive(t *testing.T) {
	backend := &contractStreamBackend{events: []ollama.StreamEvent{{Content: "ok", Done: true, DoneReason: "stop"}}}
	e := &ToolExecutor{globalAdmin: true}
	a := &Agent{ollama: backend, executor: e, model: "fixture", sessionID: "u1-fixture"}
	for _, name := range []string{"first_dynamic", "second_dynamic"} {
		e.telephonyTools = []ollama.Tool{{Function: ollama.ToolFunction{Name: name}}}
		e.hiddenTools = map[string]bool{"read_file": name == "second_dynamic"}
		if _, _, _, _, err := a.callOllama(t.Context(), "", make(chan Event, 100)); err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, tool := range backend.req.Tools {
			found[tool.Function.Name] = true
		}
		if !found[name] || found["read_file"] != (name == "first_dynamic") || name == "second_dynamic" && found["first_dynamic"] {
			t.Fatal("instruction cache retained stale tools or permission filters")
		}
	}
}
