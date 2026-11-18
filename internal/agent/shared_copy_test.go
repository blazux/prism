package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedWidgetImportCreatesIndependentCopies(t *testing.T) {
	root := t.TempDir()
	var published []string
	e := &ToolExecutor{workspaceDir: root, pluginDir: filepath.Join(root, "plugins", "board"), onPluginAdd: func(id, title, content string, cols, height int) { published = append(published, id) }}
	if err := e.writeSharedWidget("clock-shared1-0", "Clock", "first", 1, 280); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(e.pluginDir, "clock-shared1-0.html")
	if err := os.WriteFile(first, []byte("customized"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := e.writeSharedWidget("clock-shared1-0", "Clock", "second", 1, 280); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 || published[0] == published[1] {
		t.Fatal(published)
	}
	got, err := os.ReadFile(first)
	if err != nil || string(got) != "customized" {
		t.Fatalf("original lost: %q %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(e.pluginDir, published[1]+".html"))
	if err != nil || string(got) != "second" {
		t.Fatalf("new copy: %q %v", got, err)
	}
}
