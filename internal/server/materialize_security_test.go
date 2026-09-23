package server

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestBundledFilesCannotWriteOutsideWorkspace(t *testing.T) {
	for _, directory := range []string{helpWorkspaceNS, "agent_tools"} {
		t.Run(directory, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, "guide.md")
			if err := os.WriteFile(target, []byte("neighbor"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, directory)); err != nil {
				t.Fatal(err)
			}
			s := &Server{cfg: Config{WorkspaceDir: root, ToolsFS: fstest.MapFS{"agent_tools/guide.md": &fstest.MapFile{Data: []byte("replacement")}}}}
			if directory == helpWorkspaceNS {
				s.materializeHelpDocs([]helpDoc{{name: "guide.md", body: "replacement"}})
			} else {
				s.materializeAgentTools()
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "neighbor" {
				t.Fatal("bundled files escaped workspace", err)
			}
		})
	}
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "packages")
	os.WriteFile(target, []byte("private-neighbor\n"), 0600)
	if err := os.Symlink(target, filepath.Join(root, ".apt-packages")); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{WorkspaceDir: root}}
	s.ensureAptPackages([]string{"curl"})
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "private-neighbor\n" {
		t.Fatal("package merge escaped workspace", err)
	}
}
