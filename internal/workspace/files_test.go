package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfinedFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("fixture"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	os.WriteFile(filepath.Join(root, ".secret_key"), []byte("fixture-key"), 0600)
	os.Symlink(".secret_key", filepath.Join(root, "alias"))
	for _, name := range []string{"../secret", "escape/secret", ".secret_key", "alias"} {
		if _, err := ReadFile(root, name); err == nil {
			t.Fatalf("read accepted %s", name)
		}
		if _, err := Write(root, name, strings.NewReader("overwrite")); err == nil {
			t.Fatalf("write accepted %s", name)
		}
	}
	b, _ := os.ReadFile(filepath.Join(outside, "secret"))
	if string(b) != "fixture" {
		t.Fatal("outside file changed")
	}
	b, _ = os.ReadFile(filepath.Join(root, ".secret_key"))
	if string(b) != "fixture-key" {
		t.Fatal("key was truncated")
	}
	if err := WriteFile(root, "nested/file", []byte("okay")); err != nil {
		t.Fatal(err)
	}
	os.Symlink("nested", filepath.Join(root, "inside"))
	b, err := ReadFile(root, "inside/file")
	if err != nil || string(b) != "okay" {
		t.Fatalf("safe internal link broken: %v", err)
	}
	if err := Remove(root, "inside/file"); err != nil {
		t.Fatal(err)
	}
}
func TestStaticFilesStayConfined(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "file"), []byte("fixture"), 0600)
	os.Symlink(outside, filepath.Join(root, "out"))
	for _, name := range []string{"/out/file", "/"} {
		if f, err := FS(root).Open(name); err == nil {
			f.Close()
			t.Fatalf("static path accepted: %s", name)
		}
	}
}

func TestStaticSubdirectoryCannotBecomeOutsideRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	fs := SubFS{Root: root, Prefix: "data"}
	if f, err := fs.Open("/file"); err == nil {
		f.Close()
		t.Fatal("served outside file through replaced data directory")
	}
}
