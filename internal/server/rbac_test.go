package server

import (
	"context"
	"testing"

	"prism/internal/memory"
)

// TestBuildUserGuard_MemberDefaults exercises the guard classification without a
// database: a member on a store-less Server falls back to the built-in defaults.
func TestBuildUserGuard_MemberDefaults(t *testing.T) {
	s := &Server{}
	member := &memory.User{ID: 2, Email: "m@corp.com", Role: memory.RoleMember, Status: memory.StatusApproved}
	g := s.buildUserGuard(context.Background(), member)
	if g == nil {
		t.Fatal("expected a guard for a member")
	}

	allowed := func(name string, args map[string]interface{}) {
		t.Helper()
		if err := g(name, args); err != nil {
			t.Errorf("%q should be allowed for a member by default: %v", name, err)
		}
	}

	// Open by default (trusted internal deployment): members may call any tool.
	allowed("register_tool", nil)
	allowed("install_packages", nil)
	allowed("mcp", map[string]interface{}{"action": "add"})
	allowed("docker_run", nil)
	allowed("exec_command", nil)
	allowed("rag_search", nil)
	allowed("web_search", nil)
	allowed("mcp", map[string]interface{}{"action": "list"})
}

// TestToolGuard_GroupRestriction: a per-group restriction tightens the guard —
// an otherwise-open tool becomes blocked — without loosening anything.
func TestToolGuard_GroupRestriction(t *testing.T) {
	policies := map[string]string{} // everything at code defaults
	restricted := map[string]bool{"web_search": true}
	g := toolGuard(policies, restricted, "member")

	if g("web_search", nil) == nil {
		t.Error("group-restricted web_search should be blocked")
	}
	if g("rag_search", nil) != nil {
		t.Error("rag_search (open, not restricted) should be allowed")
	}
	if g("docker_run", nil) != nil {
		t.Error("docker_run (open by default, not restricted) should be allowed")
	}
	if g("register_tool", nil) != nil {
		t.Error("register_tool (open by default, not restricted) should be allowed")
	}
}

// A global admin gets a nil guard (unrestricted).
func TestBuildUserGuard_AdminUnrestricted(t *testing.T) {
	s := &Server{}
	admin := &memory.User{Role: memory.RoleGlobalAdmin}
	if s.buildUserGuard(context.Background(), admin) != nil {
		t.Error("global admin should get a nil (unrestricted) guard")
	}
	if s.buildUserGuard(context.Background(), nil) != nil {
		t.Error("nil user (legacy/service) should get a nil guard")
	}
}

// canManageRAGScope must deny a groupless personal scope in MULTI_USER mode
// (no personal RAG fallback), and keep allowing it in single-user mode — the
// exact regression this change must not introduce.
func TestCanManageRAGScope_MultiUser_NoGroup_Denied(t *testing.T) {
	member := &memory.User{ID: 5, Role: memory.RoleMember, Status: memory.StatusApproved}

	single := &Server{cfg: Config{MultiUser: false}}
	if !single.canManageRAGScope(context.Background(), member) {
		t.Error("single-user mode: groupless personal scope should remain manageable")
	}

	multi := &Server{cfg: Config{MultiUser: true}}
	if multi.canManageRAGScope(context.Background(), member) {
		t.Error("multi-user mode: groupless personal scope must NOT be manageable")
	}
}

// ragPersonalFallbackBlocked must only fire for a real personal scope ("u<id>")
// under MULTI_USER — never for "" (service identity / legacy) or a group scope,
// and never at all in single-user mode.
func TestRagPersonalFallbackBlocked(t *testing.T) {
	cases := []struct {
		name      string
		multiUser bool
		scope     string
		want      bool
	}{
		{"multi-user, service identity", true, "", false},
		{"multi-user, group scope", true, "g1", false},
		{"multi-user, personal scope", true, "u5", true},
		{"single-user, personal scope", false, "u5", false},
	}
	for _, c := range cases {
		s := &Server{cfg: Config{MultiUser: c.multiUser}}
		if got := s.ragPersonalFallbackBlocked(c.scope); got != c.want {
			t.Errorf("%s: ragPersonalFallbackBlocked(%q)=%v, want %v", c.name, c.scope, got, c.want)
		}
	}
}
