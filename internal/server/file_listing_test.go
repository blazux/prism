package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"prism/internal/workspace"
)

func TestFileDirectoryListingIsShallowAndConfined(t *testing.T) {
	root := t.TempDir()
	s := &Server{cfg: Config{WorkspaceDir: root}}
	workspace.WriteFile(root, "source/deep/report.txt", []byte("report"))
	workspace.WriteFile(root, "source/other.txt", []byte("text"))
	workspace.WriteFile(root, "source/.private", []byte("hidden"))
	os.Symlink(t.TempDir(), filepath.Join(root, "outside"))
	w := httptest.NewRecorder()
	s.handleFiles(w, httptest.NewRequest("GET", "/api/files?path=source", nil))
	var result struct{ Files []FileNode }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
		t.Fatal(w.Code, err)
	}
	byName := map[string]FileNode{}
	for _, node := range result.Files {
		byName[node.Name] = node
	}
	if len(result.Files) != 2 || !byName["deep"].IsDir || len(byName["deep"].Children) != 0 || byName["other.txt"].Path != "source/other.txt" {
		t.Fatalf("listing recursed or lost directory metadata: %+v", result)
	}
	for _, path := range []string{"../", "/etc", "outside"} {
		if _, err := s.listFileDirectory(path); err == nil {
			t.Errorf("outside directory accepted: %s", path)
		}
	}
	legacy := s.buildFileTree(root)
	if len(legacy) == 0 {
		t.Fatal("legacy recursive listing disappeared")
	}
}
