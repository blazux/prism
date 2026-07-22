package agent

import "testing"

// Personal knowledge must land in the same per-user namespace whatever the
// channel: the browser (no "u<id>-" session, grouped user → ragScope "g1"),
// Telegram ("u1-telegram"), and a shared Webex agent (falls back to the group).
func TestPersonalScope(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
		ragScope  string
		override  string
		want      string
	}{
		{"browser, grouped user, override pins user", "default", "g1", "u1", "u1"},
		{"telegram encodes the user in the session", "u1-telegram", "g1", "", "u1"},
		{"webex shared agent falls back to group", "webex-g1-abc", "g1", "", "g1"},
		{"solo user, no group", "default", "u5", "", "u5"},
	}
	for _, c := range cases {
		e := &ToolExecutor{sessionID: c.sessionID, ragScope: c.ragScope, personalScopeOverride: c.override}
		if got := e.personalScope(); got != c.want {
			t.Errorf("%s: personalScope()=%q, want %q", c.name, got, c.want)
		}
	}
}

// personalScope() must stay byte-for-byte unaffected by MULTI_USER mode — PIM
// (notes/tasks/calendar) and the pcol() agent-learnings/user-profile
// collections both depend on it, and neither should be retired by the
// MULTI_USER RAG/MCP group-only change.
func TestPersonalScope_UnaffectedByMultiUser(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
		ragScope  string
		override  string
		want      string
	}{
		{"browser, grouped user, override pins user", "default", "g1", "u1", "u1"},
		{"telegram encodes the user in the session", "u1-telegram", "g1", "", "u1"},
		{"webex shared agent falls back to group", "webex-g1-abc", "g1", "", "g1"},
		{"solo user, no group", "default", "u5", "", "u5"},
	}
	for _, c := range cases {
		e := &ToolExecutor{sessionID: c.sessionID, ragScope: c.ragScope, personalScopeOverride: c.override, multiUser: true}
		if got := e.personalScope(); got != c.want {
			t.Errorf("%s (multiUser=true): personalScope()=%q, want %q", c.name, got, c.want)
		}
	}
}

// SecretsScope is a thin exported wrapper around personalScope(), needed so
// package server (ws.go's async secret-request callback) can resolve the
// same per-user/per-group scope — it must never diverge from personalScope().
func TestSecretsScope_MatchesPersonalScope(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
		ragScope  string
		override  string
	}{
		{"browser, grouped user, override pins user", "default", "g1", "u1"},
		{"telegram encodes the user in the session", "u1-telegram", "g1", ""},
		{"webex shared agent falls back to group", "webex-g1-abc", "g1", ""},
		{"solo user, no group", "default", "u5", ""},
	}
	for _, c := range cases {
		e := &ToolExecutor{sessionID: c.sessionID, ragScope: c.ragScope, personalScopeOverride: c.override}
		if got, want := e.SecretsScope(), e.personalScope(); got != want {
			t.Errorf("%s: SecretsScope()=%q, personalScope()=%q — must match", c.name, got, want)
		}
	}
}
