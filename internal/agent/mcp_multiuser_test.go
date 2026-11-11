package agent

import "testing"

func TestMCPRegistryIgnoresWorkspace(t *testing.T) {
	for _, multi := range []bool{false, true} {
		for _, group := range []string{"", "g1", "u5", "global"} {
			for _, workspace := range []string{"default", "scan", "u5-board", "room-g1"} {
				e := &ToolExecutor{sessionID: workspace, ragScope: group, personalScopeOverride: "u5", multiUser: multi}
				want := "global"
				if multi {
					want = ""
					if group == "g1" {
						want = group
					}
				}
				if got := e.mcpScope(); got != want {
					t.Fatalf("multi=%v group=%q workspace=%q: scope=%q, want %q", multi, group, workspace, got, want)
				}
			}
		}
	}
}
