package agent

import (
	"os"
	"path/filepath"
	"testing"

	"prism/internal/memory"
)

func TestResolveShareGroup(t *testing.T) {
	e := &ToolExecutor{}
	// No groups.
	if _, err := e.resolveShareGroup(""); err == nil {
		t.Error("expected error with no groups")
	}
	// One group → auto.
	e.sharingGroups = []memory.Membership{{GroupID: 5, GroupName: "Backend"}}
	if g, err := e.resolveShareGroup(""); err != nil || g.GroupID != 5 {
		t.Errorf("single group should auto-resolve: %v %+v", err, g)
	}
	// Several → require a name, matched case-insensitively.
	e.sharingGroups = []memory.Membership{{GroupID: 5, GroupName: "Backend"}, {GroupID: 6, GroupName: "Data"}}
	if _, err := e.resolveShareGroup(""); err == nil {
		t.Error("multiple groups without name should error")
	}
	if g, err := e.resolveShareGroup("data"); err != nil || g.GroupID != 6 {
		t.Errorf("name match failed: %v %+v", err, g)
	}
	if _, err := e.resolveShareGroup("nope"); err == nil {
		t.Error("unknown group name should error")
	}
}

func TestReadBoardWidgets(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "clock.html"), []byte("<div>clock</div>"), 0644)
	os.WriteFile(filepath.Join(dir, "clock.meta.json"), []byte(`{"title":"Clock","cols":2,"height":300}`), 0644)
	os.WriteFile(filepath.Join(dir, "notes.html"), []byte("<div>notes</div>"), 0644) // no meta → defaults
	e := &ToolExecutor{pluginDir: dir}

	all := e.readBoardWidgets("")
	if len(all) != 2 {
		t.Fatalf("want 2 widgets, got %d", len(all))
	}
	one := e.readBoardWidgets("clock")
	if len(one) != 1 || one[0].Title != "Clock" || one[0].Cols != 2 || one[0].Content != "<div>clock</div>" {
		t.Errorf("single-widget read wrong: %+v", one)
	}
	// Missing meta falls back to id/defaults.
	n := e.readBoardWidgets("notes")
	if len(n) != 1 || n[0].Title != "notes" || n[0].Cols != 1 || n[0].Height != 280 {
		t.Errorf("default meta wrong: %+v", n)
	}
	if got := e.readBoardWidgets("ghost"); got != nil {
		t.Errorf("nonexistent widget should be empty, got %+v", got)
	}
}
