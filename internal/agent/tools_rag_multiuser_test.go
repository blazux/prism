package agent

import (
	"context"
	"testing"
)

// ragBlocked mirrors server.ragPersonalFallbackBlocked: true only when
// MULTI_USER is set and the executor's scope isn't a group scope.
func TestRagBlocked(t *testing.T) {
	cases := []struct {
		name      string
		multiUser bool
		ragScope  string
		want      bool
	}{
		{"multi-user, service identity", true, "", false},
		{"multi-user, group scope", true, "g1", false},
		{"multi-user, personal scope", true, "u5", true},
		{"single-user, personal scope", false, "u5", false},
	}
	for _, c := range cases {
		e := &ToolExecutor{ragScope: c.ragScope, multiUser: c.multiUser}
		if got := e.ragBlocked(); got != c.want {
			t.Errorf("%s: ragBlocked()=%v, want %v", c.name, got, c.want)
		}
	}
}

// The 5 RAG tools that touch ragScope/col() directly must refuse with the
// standard message when blocked, before ever touching e.ragStore (which is
// nil in this test — a nil-pointer panic here would mean the guard isn't
// actually first).
func TestRagTools_RefuseWhenBlocked(t *testing.T) {
	e := &ToolExecutor{ragScope: "u5", multiUser: true}
	ctx := context.Background()

	if msg, _, _ := e.ragSearch(ctx, "q", "docs", 5); msg != ragBlockedMsg {
		t.Errorf("ragSearch: got %q, want %q", msg, ragBlockedMsg)
	}
	if msg, _ := e.ragIngest(ctx, "docs", "src", "content", ""); msg != ragBlockedMsg {
		t.Errorf("ragIngest: got %q, want %q", msg, ragBlockedMsg)
	}
	if msg, _ := e.ragListCollections(ctx); msg != ragBlockedMsg {
		t.Errorf("ragListCollections: got %q, want %q", msg, ragBlockedMsg)
	}
	if msg, _ := e.ragListDocuments(ctx, "docs"); msg != ragBlockedMsg {
		t.Errorf("ragListDocuments: got %q, want %q", msg, ragBlockedMsg)
	}
	if msg, _ := e.ragDelete(ctx, "docs", ""); msg != ragBlockedMsg {
		t.Errorf("ragDelete: got %q, want %q", msg, ragBlockedMsg)
	}
}

// Grouped multi-user (ragScope="g1") must NOT be blocked — falls through to
// the normal "RAG not available (Postgres not configured)" path since
// e.ragStore is nil here, proving the guard doesn't over-fire on a real group.
func TestRagTools_NotBlockedForGroup(t *testing.T) {
	e := &ToolExecutor{ragScope: "g1", multiUser: true}
	ctx := context.Background()

	if msg, _ := e.ragListCollections(ctx); msg == ragBlockedMsg {
		t.Errorf("ragListCollections: group scope should not be blocked, got %q", msg)
	}
}
