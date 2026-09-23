package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreviewAndBrowserFilesStayInsideWorkspace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "private")
	if err := os.WriteFile(target, []byte("neighbor"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".browser_exec.py")); err != nil {
		t.Fatal(err)
	}
	e := &ToolExecutor{workspaceDir: root, pluginDir: filepath.Join(root, "plugins", "default")}
	if err := e.writeManagedFile(filepath.Join(root, ".browser_exec.py"), []byte("replacement")); err == nil {
		t.Fatal("browser script escaped")
	}
	if err := os.Symlink(outside, filepath.Join(root, ".screenshots")); err != nil {
		t.Fatal(err)
	}
	if got := extractScreenshotImages(`[{"action":"screenshot","status":"ok","url":"/screenshots/private"}]`, root); len(got) != 0 {
		t.Fatal("outside screenshot read")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "neighbor" {
		t.Fatal("outside data changed", err)
	}
}
