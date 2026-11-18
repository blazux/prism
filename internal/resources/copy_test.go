package resources

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWidgetCopiesPreserveExistingAndOrphanFiles(t *testing.T) {
	root := t.TempDir()
	dir := "plugins/board"
	id, err := CreateWidgetCopy(root, dir, "clock-shared1-0", []byte("original"), []byte(`{"locked":false}`))
	if err != nil || id != "clock-shared1-0" {
		t.Fatal(id, err)
	}
	html := filepath.Join(root, dir, id+".html")
	meta := filepath.Join(root, dir, id+".meta.json")
	custom := []byte(`{"locked":true,"cols":3,"height":700}`)
	if err := os.WriteFile(html, []byte("customized"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, custom, 0644); err != nil {
		t.Fatal(err)
	}
	// An existing HTML-only copy must be preserved as well.
	if err := os.WriteFile(filepath.Join(root, dir, id+"-2.html"), []byte("orphan"), 0644); err != nil {
		t.Fatal(err)
	}
	copyID, err := CreateWidgetCopy(root, dir, id, []byte("fresh"), []byte(`{"locked":false}`))
	if err != nil || copyID != id+"-3" {
		t.Fatal(copyID, err)
	}
	for path, want := range map[string]string{html: "customized", meta: string(custom), filepath.Join(root, dir, id+"-2.html"): "orphan", filepath.Join(root, dir, copyID+".html"): "fresh"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", path, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, dir, id+"-2.meta.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected metadata left behind: %v", err)
	}
}

func TestWidgetCopyRejectsEscapingOrPrivatePaths(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"../outside", ".secret_key"} {
		if _, err := CreateWidgetCopy(root, dir, "copy", []byte("x"), []byte("{}")); err == nil {
			t.Fatal("accepted", dir)
		}
	}
}
