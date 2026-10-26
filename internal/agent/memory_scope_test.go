package agent

import (
	"strings"
	"testing"
)

func TestSkillsFollowMemoryOwnerAcrossWorkspaces(t *testing.T) {
	dir := t.TempDir()
	executor := func(session, scope string, multi bool) *ToolExecutor {
		return &ToolExecutor{workspaceDir: dir, sessionID: session, personalScopeOverride: scope, multiUser: multi}
	}
	legacy := executor("default", "", false)
	if _, err := legacy.skillTool("save", "legacy", "", "", "Unattributed procedure"); err != nil {
		t.Fatal(err)
	}
	owner := executor("u1-project", "u1", true)
	if _, err := owner.skillTool("save", "deploy", "", "Deploy a service", "Owner one procedure"); err != nil {
		t.Fatal(err)
	}
	sameOwner := executor("u1-telegram", "", true)
	if got := sameOwner.SkillsIndex(); !strings.Contains(got, "deploy") || strings.Contains(got, "legacy") {
		t.Fatalf("wrong personal skill index: %s", got)
	}
	for _, other := range []*ToolExecutor{
		executor("u10-project", "u10", true),
		executor("room-g1", "g1", true),
	} {
		if got := other.SkillsIndex(); got != "" {
			t.Fatalf("another owner saw skills: %s", got)
		}
		if got, err := other.skillTool("get", "deploy", "", "", ""); err != nil || strings.Contains(got, "Owner one") {
			t.Fatalf("another owner read skill: %s, %v", got, err)
		}
		// Same name must not overwrite or delete the original owner's procedure.
		if _, err := other.skillTool("save", "deploy", "", "", "Other procedure"); err != nil {
			t.Fatal(err)
		}
		if _, err := other.skillTool("delete", "deploy", "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := owner.skillTool("get", "deploy", "", "", ""); err != nil || !strings.Contains(got, "Owner one procedure") {
		t.Fatalf("owner's skill changed: %s, %v", got, err)
	}
	group := executor("room-g1", "g1", true)
	if _, err := group.skillTool("save", "handover", "", "", "Group procedure"); err != nil {
		t.Fatal(err)
	}
	if got := executor("webex-g1-space", "g1", true).SkillsIndex(); !strings.Contains(got, "handover") {
		t.Fatalf("group channels do not share skills: %s", got)
	}
	if got := executor("room-g10", "g10", true).SkillsIndex(); got != "" {
		t.Fatalf("another group saw skills: %s", got)
	}
	if got := legacy.SkillsIndex(); !strings.Contains(got, "legacy") || strings.Contains(got, "deploy") || strings.Contains(got, "handover") {
		t.Fatalf("single-user library changed: %s", got)
	}
}

func TestMultiUserMemoryRequiresIdentity(t *testing.T) {
	for _, scope := range []string{"", "global", "voice", "u0", "g0", "u1/../u2", "u1-extra"} {
		e := &ToolExecutor{multiUser: true, personalScopeOverride: scope, workspaceDir: t.TempDir()}
		if _, err := e.durableMemoryScope(); err == nil {
			t.Errorf("unidentified memory accepted scope %q", scope)
		}
		if _, err := e.skillTool("save", "test", "", "", "secret"); err == nil {
			t.Errorf("unidentified skill write accepted scope %q", scope)
		}
	}
}
