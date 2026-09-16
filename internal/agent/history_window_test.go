package agent

import (
	"context"
	"testing"

	"prism/internal/memory"
	"prism/internal/ollama"
)

// rows builds a history tail from a role sequence ("u"/"a"/"t"), ids from 1.
func rows(seq ...string) []memory.HistoryEntry {
	roles := map[string]string{"u": "user", "a": "assistant", "t": "tool"}
	out := make([]memory.HistoryEntry, 0, len(seq))
	for i, s := range seq {
		out = append(out, memory.HistoryEntry{ID: int64(i + 1), Role: roles[s]})
	}
	return out
}

func roleSeq(entries []memory.HistoryEntry) string {
	short := map[string]string{"user": "u", "assistant": "a", "tool": "t"}
	s := ""
	for _, e := range entries {
		s += short[e.Role]
	}
	return s
}

func TestClampHistoryTail(t *testing.T) {
	cases := []struct {
		name      string
		in        []memory.HistoryEntry
		maxRows   int
		want      string
		truncated bool
	}{
		{"under cap is untouched", rows("u", "a", "u", "a"), 10, "uaua", false},
		{"exactly at cap is untouched", rows("u", "a"), 2, "ua", false},
		{"no cap is untouched", rows("u", "a", "u"), 0, "uau", false},
		// The naive tail would start on "a" (an answer with no question) or on
		// "t" (a tool result whose assistant tool_calls got cut) — both are
		// advanced to the next user row.
		{"cut advances past a dangling assistant", rows("u", "a", "u", "a", "u", "a"), 3, "ua", true},
		{"cut advances past an orphan tool result", rows("u", "a", "t", "a", "u", "a"), 4, "ua", true},
		{"window with no user row is dropped", rows("u", "a", "t", "t"), 3, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, truncated := clampHistoryTail(c.in, c.maxRows)
			if roleSeq(got) != c.want {
				t.Errorf("roles = %q, want %q", roleSeq(got), c.want)
			}
			if truncated != c.truncated {
				t.Errorf("truncated = %v, want %v", truncated, c.truncated)
			}
		})
	}
}

// budgetBackend reports a fixed context budget.
type budgetBackend struct{ budget int }

func (b *budgetBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
}
func (b *budgetBackend) Ping(ctx context.Context) error                   { return nil }
func (b *budgetBackend) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (b *budgetBackend) ContextBudgetChars() int                          { return b.budget }

// A channel agent's budget must tighten the window, never widen it past what
// the backend can actually take.
func TestEffectiveHistoryBudget_OverrideOnlyLowers(t *testing.T) {
	orig := liveContextCharBudget
	defer func() { liveContextCharBudget = orig }()
	liveContextCharBudget = 150_000

	cases := []struct {
		name     string
		backend  int
		override int
		want     int
	}{
		{"no backend cap, no override", 0, 0, 150_000},
		{"override tightens the default", 0, 30_000, 30_000},
		{"backend cap tightens the default", 50_000, 0, 50_000},
		{"override tightens the backend cap", 50_000, 30_000, 30_000},
		{"override above the backend cap is ignored", 20_000, 30_000, 20_000},
		{"override above the default is ignored", 0, 900_000, 150_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Agent{
				ollama:         &budgetBackend{budget: c.backend},
				limitsOverride: Limits{HistoryBudgetChars: c.override},
			}
			if got := a.effectiveHistoryBudget(); got != c.want {
				t.Errorf("effectiveHistoryBudget() = %d, want %d", got, c.want)
			}
		})
	}
}
