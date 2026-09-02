package agent

import "testing"

// groupSecretsScope returns the extra secrets tier a session receives beyond
// its own SecretsScope: the group's shared secrets for a member's personal
// session, and nothing everywhere else — a room session already runs under
// the group scope (adding it again would double-fetch), and non-group rag
// scopes ("", "u5", "global", board ids that merely start with g) must never
// be mistaken for a group.
func TestGroupSecretsScope(t *testing.T) {
	cases := []struct {
		name     string
		ragScope string
		override string
		session  string
		want     string
	}{
		{"member personal session", "g1", "u5", "default", "g1"},
		{"member telegram session", "g1", "", "u5-telegram", "g1"},
		{"room session (already group-scoped)", "g1", "", "room-g1", ""},
		{"legacy single-user", "", "", "default", ""},
		{"multi-user groupless", "u5", "u5", "default", ""},
		{"global scope is not a group", "global", "u5", "default", ""},
		{"g-prefixed non-numeric scope", "garden", "u5", "default", ""},
	}
	for _, c := range cases {
		e := &ToolExecutor{sessionID: c.session, ragScope: c.ragScope, personalScopeOverride: c.override}
		if got := e.groupSecretsScope(); got != c.want {
			t.Errorf("%s: groupSecretsScope()=%q, want %q", c.name, got, c.want)
		}
	}
}
