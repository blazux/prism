package server

import (
	"slices"
	"testing"

	"prism/internal/agent"
)

func TestHistorySearchStaysWithinMemoryOwner(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	sessions := []string{"u1-main", "u1-assistant", "u1-telegram", "u10-main", "u2-main", "room-g1", "webex-g1-space", "room-g10", "webex-g10-space", "default"}
	for _, session := range sessions {
		if _, err := ms.AppendMessage(ctx, session, "user", "memoryfixture discussion", nil); err != nil {
			t.Fatal(err)
		}
	}
	// Many competing private hits must not fill LIMIT before owner filtering.
	for range 20 {
		if _, err := ms.AppendMessage(ctx, "u2-main", "assistant", "memoryfixture memoryfixture memoryfixture", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		scope string
		want  []string
	}{
		{"u1", []string{"u1-main", "u1-assistant", "u1-telegram"}},
		{"u10", []string{"u10-main"}},
		{"g1", []string{"room-g1", "webex-g1-space"}},
		{"g10", []string{"room-g10", "webex-g10-space"}},
		{"u99", nil},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			hits, err := ms.SearchHistory(ctx, "memoryfixture", 50, tc.scope)
			if err != nil || len(hits) != len(tc.want) {
				t.Fatalf("search returned %d hits, want %d: %v", len(hits), len(tc.want), err)
			}
			for _, hit := range hits {
				if !slices.Contains(tc.want, hit.SessionID) {
					t.Fatalf("private history leaked: %+v", hit)
				}
			}
			hits, err = ms.SearchHistory(ctx, "memoryfixture", 1, tc.scope)
			if err != nil || (len(tc.want) > 0 && len(hits) != 1) {
				t.Fatalf("limit displaced permitted results: %v, %v", hits, err)
			}
		})
	}
	hits, err := ms.SearchHistory(ctx, "memoryfixture", 50, "")
	if err != nil || len(hits) != len(sessions)+20 {
		t.Fatalf("single-user search changed: %d hits, %v", len(hits), err)
	}
	// Clearing one conversation removes its searchable text, not other spaces.
	a := agent.New(nil, nil, "", ms, "")
	a.SetSession("u1-main", "")
	if err := ms.SetConfig(ctx, "conversation_summary_u1-main", "Old summary"); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetConfig(ctx, "conversation_summary_u1-assistant", "Other workspace summary"); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetHistory(); err != nil {
		t.Fatal(err)
	}
	if got := ms.GetSummary(ctx, "u1-main"); got != "" {
		t.Fatalf("reset retained summary: %s", got)
	}
	if got := ms.GetSummary(ctx, "u1-assistant"); got != "Other workspace summary" {
		t.Fatalf("reset changed another workspace summary: %s", got)
	}
	hits, err = ms.SearchHistory(ctx, "memoryfixture", 50, "u1")
	if err != nil || len(hits) != 2 {
		t.Fatalf("reset affected other conversations: %v, %v", hits, err)
	}
}
