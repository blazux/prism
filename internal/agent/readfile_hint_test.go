package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileNotFoundHint checks that a missing path yields an error that
// teaches: the closest-name suggestion for a typo plus the directory listing,
// instead of a bare "no such file".
func TestReadFileNotFoundHint(t *testing.T) {
	ws := t.TempDir()
	for _, f := range []string{"notes.md", "config.yaml", "main.py"} {
		if err := os.WriteFile(filepath.Join(ws, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	e := &ToolExecutor{workspaceDir: ws}

	_, err := e.readFile("notez.md") // one-char typo of notes.md
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean notes.md") {
		t.Errorf("expected closest-name suggestion, got: %s", msg)
	}
	if !strings.Contains(msg, "config.yaml") || !strings.Contains(msg, "main.py") {
		t.Errorf("expected sibling listing, got: %s", msg)
	}

	// An unrelated name should still list siblings but not force a bogus suggestion.
	_, err = e.readFile("zzzzzzzz.dat")
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("should not suggest an unrelated file, got: %s", err.Error())
	}
}
