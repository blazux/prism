package agent

import "testing"

// mcpFallbackScopes drives both AllDynamicTools' tool-merge loop and
// mcpSessionFor's tool-resolution loop — this is the single place the
// MULTI_USER personal-MCP-fallback retirement is expressed, so it's the one
// worth testing directly (mcp.Manager itself has no DB-free seam to fake
// per-scope tool data through).
func TestMcpFallbackScopes(t *testing.T) {
	cases := []struct {
		name      string
		multiUser bool
		ragScope  string
		override  string
		want      []string
	}{
		{"single-user: personal, group, then global fallback", false, "g1", "u5", []string{"u5", "g1", "global"}},
		{"single-user: personal then empty ragScope, global fallback", false, "", "u5", []string{"u5", "", "global"}},
		{"multi-user: group only, personal tier dropped", true, "g1", "u5", []string{"g1"}},
		{"multi-user, groupless: only the (empty) rag scope", true, "", "u5", []string{""}},
	}
	for _, c := range cases {
		e := &ToolExecutor{ragScope: c.ragScope, personalScopeOverride: c.override, multiUser: c.multiUser}
		got := e.mcpFallbackScopes()
		if len(got) != len(c.want) {
			t.Fatalf("%s: mcpFallbackScopes()=%v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: mcpFallbackScopes()=%v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}
