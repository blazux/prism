package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestExecutorWithPlugins is newTestExecutor plus a plugin (widget) dir,
// wired the way a real dashboard session sets it up (SetPluginDir).
func newTestExecutorWithPlugins(t *testing.T) (*ToolExecutor, string, string) {
	t.Helper()
	e, workspaceDir := newTestExecutor(t)
	pluginDir := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	e.SetPluginDir(pluginDir)
	return e, workspaceDir, pluginDir
}

func writeWidget(t *testing.T, pluginDir, id, html string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(pluginDir, id+".html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, id+".meta.json"), []byte(`{"title":"`+id+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
}

// Removing a widget that fetches a data/ polling file no other widget uses
// must surface a note pointing at it — and never delete the data file
// itself, since it could still be wanted or written by a cron job/tool with
// no widget involved at all.
func TestRemoveUIPlugin_NotesOrphanedDataFile(t *testing.T) {
	e, workspaceDir, pluginDir := newTestExecutorWithPlugins(t)
	if err := os.MkdirAll(filepath.Join(workspaceDir, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "data", "stocks.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	writeWidget(t, pluginDir, "stocks", `<script>fetch('/data/stocks.json').then(...)</script>`)

	msg, err := e.removeUIPlugin("stocks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "data/stocks.json") {
		t.Errorf("expected orphaned-data note mentioning data/stocks.json, got %q", msg)
	}
	if !strings.Contains(msg, "Ask the user") {
		t.Errorf("expected note to require asking the user before deleting, got %q", msg)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "data", "stocks.json")); err != nil {
		t.Error("the data file itself must NOT be deleted automatically")
	}
}

// A data file still referenced by another live widget must never be flagged
// as orphaned — it's still in use.
func TestRemoveUIPlugin_NoNoteWhenDataStillSharedByAnotherWidget(t *testing.T) {
	e, workspaceDir, pluginDir := newTestExecutorWithPlugins(t)
	if err := os.MkdirAll(filepath.Join(workspaceDir, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "data", "shared.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	writeWidget(t, pluginDir, "widget-a", `<script>fetch('/data/shared.json')</script>`)
	writeWidget(t, pluginDir, "widget-b", `<script>fetch('/data/shared.json')</script>`)

	msg, err := e.removeUIPlugin("widget-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(msg, "shared.json") {
		t.Errorf("data file still used by widget-b must not be flagged as orphaned, got %q", msg)
	}
}

// A widget with no data/ reference at all (e.g. fully static/self-contained)
// must remove cleanly with no note.
func TestRemoveUIPlugin_NoDataRefsNoNote(t *testing.T) {
	e, _, pluginDir := newTestExecutorWithPlugins(t)
	writeWidget(t, pluginDir, "clock", `<div id="clock"></div><script>setInterval(()=>{},1000)</script>`)

	msg, err := e.removeUIPlugin("clock")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "removed") || strings.Contains(msg, "Note:") {
		t.Errorf("expected a plain removal message with no orphan note, got %q", msg)
	}
}

// A widget dragged to exactly the left/top edge (x:0, y:0) must keep that
// position across the read-mutate-remarshal roundtrip updateUIPlugin does on
// every content update. Before pluginMeta.X/Y switched to pointers (like Open
// already was), `omitempty` on a plain float64 could not tell "explicitly 0"
// apart from "never positioned" and silently dropped the "x"/"y" keys.
func TestPluginMeta_ZeroPositionRoundTrips(t *testing.T) {
	zero := 0.0
	before, err := json.Marshal(pluginMeta{Title: "Corner", Cols: 1, Height: 200, X: &zero, Y: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), `"x":0`) || !strings.Contains(string(before), `"y":0`) {
		t.Fatalf("marshal dropped the explicit zero position: %s", before)
	}

	// Simulate updateUIPlugin's own sequence: unmarshal the existing file,
	// mutate an unrelated field, remarshal.
	var m pluginMeta
	if err := json.Unmarshal(before, &m); err != nil {
		t.Fatal(err)
	}
	m.Title = "Corner Updated"
	after, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), `"x":0`) || !strings.Contains(string(after), `"y":0`) {
		t.Errorf("re-marshal after an unrelated field update dropped the zero position: %s", after)
	}
}

// A "not found" on update/remove must list the widgets that DO exist: the
// model routinely guesses ids instead of calling list first, and a bare
// "not found" just sends it guessing again — the real ids in the error let
// it self-correct on the next call.
func TestUpdateUIPlugin_NotFoundListsExistingWidgets(t *testing.T) {
	e, _, pluginDir := newTestExecutorWithPlugins(t)
	writeWidget(t, pluginDir, "ipam-viewer", `<div></div>`)
	writeWidget(t, pluginDir, "sys-monitor", `<div></div>`)

	_, _, err := e.updateUIPlugin(context.Background(), "ipam-view", "", "<p>x</p>", 0, 0)
	if err == nil {
		t.Fatal("expected an error for an unknown widget id")
	}
	if !strings.Contains(err.Error(), "ipam-viewer") || !strings.Contains(err.Error(), "sys-monitor") {
		t.Errorf("not-found error should list existing widget ids, got %q", err)
	}
}

func TestRemoveUIPlugin_NotFoundListsExistingWidgets(t *testing.T) {
	e, _, pluginDir := newTestExecutorWithPlugins(t)
	writeWidget(t, pluginDir, "ipam-viewer", `<div></div>`)

	_, err := e.removeUIPlugin("ipamviewer")
	if err == nil {
		t.Fatal("expected an error for an unknown widget id")
	}
	if !strings.Contains(err.Error(), "ipam-viewer") {
		t.Errorf("not-found error should list existing widget ids, got %q", err)
	}
}
