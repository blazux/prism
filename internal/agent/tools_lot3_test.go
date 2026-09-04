package agent

import (
	"context"
	"strings"
	"testing"

	"prism/internal/memory"
)

func TestMCPWriteScope(t *testing.T) {
	single := &ToolExecutor{}
	if scope, denial := single.mcpWriteScope(); denial != "" || scope != single.mcpStorageScope() {
		t.Errorf("single-user: got (%q, %q)", scope, denial)
	}
	noGroup := &ToolExecutor{multiUser: true, ragScope: "u7"}
	if _, denial := noGroup.mcpWriteScope(); !strings.Contains(denial, "not in a group") {
		t.Errorf("multi-user without group: %q", denial)
	}
	member := &ToolExecutor{multiUser: true, ragScope: "g5", sharingGroups: []memory.Membership{{GroupID: 5, GroupName: "NOC", Role: "member"}}}
	if _, denial := member.mcpWriteScope(); !strings.Contains(denial, "NOC") || !strings.Contains(denial, "group admin") {
		t.Errorf("member must be told who can, naming the group: %q", denial)
	}
	admin := &ToolExecutor{multiUser: true, ragScope: "g5", sharingGroups: []memory.Membership{{GroupID: 5, GroupName: "NOC", Role: "admin"}}}
	if scope, denial := admin.mcpWriteScope(); denial != "" || scope != "g5" {
		t.Errorf("group admin: got (%q, %q)", scope, denial)
	}
	global := &ToolExecutor{multiUser: true, ragScope: "g5", globalAdmin: true}
	if scope, denial := global.mcpWriteScope(); denial != "" || scope != "g5" {
		t.Errorf("global admin: got (%q, %q)", scope, denial)
	}
}

// A plain member's agent must not write to the group knowledge base (the HTTP
// path already enforced this; the tool path did not) — while personal
// collections stay writable.
func TestRAGReadOnly_GroupWritesRefused(t *testing.T) {
	e := &ToolExecutor{ragScope: "g5", ragReadOnly: true}
	if out, _ := e.ragIngest(context.Background(), "docs", "x", "y", ""); out != ragReadOnlyMsg {
		t.Errorf("group ingest must be refused: %q", out)
	}
	if out, _ := e.ragDelete(context.Background(), "docs", ""); out != ragReadOnlyMsg {
		t.Errorf("group delete must be refused: %q", out)
	}
	if out, _ := e.ragIngest(context.Background(), learningsCollection, "x", "y", ""); out == ragReadOnlyMsg {
		t.Errorf("personal collections must not be caught by the group guard")
	}
}

func TestAgentSettings_ContextAndStore(t *testing.T) {
	room := &ToolExecutor{sessionID: "room-g3"}
	if out, _ := room.agentSettings(context.Background(), "get", nil); !strings.Contains(out, "admin console") {
		t.Errorf("group agent must redirect to the admin console: %q", out)
	}
	e := &ToolExecutor{sessionID: "u1-main"}
	if out, _ := e.agentSettings(context.Background(), "get", nil); !strings.Contains(out, "not available") {
		t.Errorf("no store: %q", out)
	}
	if _, err := e.agentSettings(context.Background(), "frobnicate", nil); err == nil {
		t.Error("unknown action must error")
	}
}

func TestServerTool_MissingSaysSo(t *testing.T) {
	e := &ToolExecutor{}
	out, _ := e.serverTool(context.Background(), "webhook", nil)
	if !strings.Contains(out, "not available in this context") {
		t.Errorf("%q", out)
	}
	e.SetServerTools(map[string]ServerTool{"webhook": func(context.Context, map[string]any) (string, error) { return "ok", nil }})
	if out, _ := e.serverTool(context.Background(), "webhook", nil); out != "ok" {
		t.Errorf("injected tool not dispatched: %q", out)
	}
}
