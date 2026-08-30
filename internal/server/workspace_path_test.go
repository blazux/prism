package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeWorkspacePath(t *testing.T) {
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, "sub"), 0755)
	os.WriteFile(filepath.Join(ws, "sub", "a.txt"), []byte("x"), 0644)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "leak"), []byte("secret"), 0644)
	// The agent's sandbox shares the volume: `ln -s / /workspace/esc`.
	if err := os.Symlink(outside, filepath.Join(ws, "esc")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}

	ok := []string{"sub/a.txt", "sub/new.txt", "brand/new/dir/file.py", "/sub/a.txt", "."}
	for _, p := range ok {
		if _, err := safeWorkspacePath(ws, p); err != nil {
			t.Errorf("%q: expected ok, got %v", p, err)
		}
	}
	bad := []string{"../x", "sub/../../x", ".secret_key", "sub/.secret_key", "esc/leak", "esc/new.txt"}
	for _, p := range bad {
		if full, err := safeWorkspacePath(ws, p); err == nil {
			t.Errorf("%q: expected rejection, got %q", p, full)
		}
	}
}
