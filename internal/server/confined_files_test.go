package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWidgetFilesAndUploadsCannotFollowOutsideLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	s := &Server{cfg: Config{WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins")}}
	if err := os.WriteFile(filepath.Join(outside, "private.html"), []byte("neighbor"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, s.cfg.PluginDir); err != nil {
		t.Fatal(err)
	}
	if got := s.loadPlugins(s.cfg.PluginDir); len(got) != 0 {
		t.Fatal("outside widgets were loaded")
	}
	if err := s.updatePluginMeta(filepath.Join(s.cfg.PluginDir, "private.meta.json"), func(m map[string]any) { m["title"] = "changed" }); err == nil {
		t.Fatal("outside metadata write allowed")
	}
	if err := s.removeManaged(filepath.Join(s.cfg.PluginDir, "private.html")); err == nil {
		t.Fatal("outside widget removed")
	}
	if err := os.Symlink(outside, filepath.Join(root, "uploads")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if s.saveChatUpload(source, "private.html") != "" {
		t.Fatal("upload accepted through outside directory")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 1 {
		t.Fatal("outside directory mutated", err)
	}
	data, err := os.ReadFile(filepath.Join(outside, "private.html"))
	if err != nil || string(data) != "neighbor" {
		t.Fatal("outside data changed", err)
	}
}
