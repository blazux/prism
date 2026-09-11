package server

import (
	"net/http/httptest"
	"testing"

	"prism/internal/memory"
)

// ragScopeForRequest must refuse (ok=false) a groupless personal scope under
// MULTI_USER — the retired fallback — while leaving single-user mode's
// personal-scope resolution completely unaffected. Both cases run without a
// store, so there is no group to consult either way; the only difference is
// the deployment mode.
func TestRagScopeForRequest_MultiUser_NoGroup(t *testing.T) {
	member := &memory.User{ID: 5, Role: memory.RoleMember, Status: memory.StatusApproved}

	single := &Server{cfg: Config{MultiUser: false}}
	r := withUser(httptest.NewRequest("GET", "/api/rag/collections", nil), member)
	scope, manage, ok := single.ragScopeForRequest(r)
	if !ok || scope != "u5" || !manage {
		t.Errorf("single-user mode: got scope=%q manage=%v ok=%v, want scope=%q manage=true ok=true", scope, manage, ok, "u5")
	}

	multi := &Server{cfg: Config{MultiUser: true}}
	r = withUser(httptest.NewRequest("GET", "/api/rag/collections", nil), member)
	if _, _, ok := multi.ragScopeForRequest(r); ok {
		t.Error("multi-user mode: groupless personal scope must be refused (ok=false)")
	}
}

// The document delete is restricted by collection-name prefix: a scoped tenant
// gets "<scope>--", the unscoped legacy mode an empty prefix (matches all).
func TestRagScopePrefix(t *testing.T) {
	if got := ragScopePrefix("g3"); got != "g3--" {
		t.Errorf("ragScopePrefix(g3) = %q, want %q", got, "g3--")
	}
	if got := ragScopePrefix(""); got != "" {
		t.Errorf("ragScopePrefix(\"\") = %q, want empty", got)
	}
	// Must agree with how collections are actually stored.
	if got := scopeCollection("g3", "docs"); got != "g3--docs" {
		t.Errorf("scopeCollection = %q, prefix check would miss it", got)
	}
}
