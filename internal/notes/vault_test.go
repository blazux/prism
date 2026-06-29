package notes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := &VaultProvider{Dir: dir}
	ctx := context.Background()

	// Create: filename derives from title, body (incl. frontmatter) is verbatim.
	id, err := p.Save(ctx, "", "My Note", "---\ntags: a, b\n---\nhello [[Other]]", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "My Note.md" {
		t.Fatalf("id = %q, want My Note.md", id)
	}

	items, err := p.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list err=%v n=%d", err, len(items))
	}
	if items[0].Title != "My Note" {
		t.Fatalf("title = %q", items[0].Title)
	}
	if items[0].Tags != "a, b" {
		t.Fatalf("tags = %q", items[0].Tags)
	}
	if !strings.Contains(items[0].Body, "[[Other]]") {
		t.Fatalf("body not preserved: %q", items[0].Body)
	}

	// Update with a new title renames the file and removes the old one.
	id2, err := p.Save(ctx, id, "Renamed", "new body", "")
	if err != nil {
		t.Fatal(err)
	}
	if id2 != "Renamed.md" {
		t.Fatalf("renamed id = %q", id2)
	}
	if _, err := os.Stat(filepath.Join(dir, "My Note.md")); !os.IsNotExist(err) {
		t.Fatal("old file should be gone after rename")
	}

	// Path traversal is rejected.
	if _, err := p.resolve("../outside.md"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}

	if err := p.Delete(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if items, _ := p.List(ctx); len(items) != 0 {
		t.Fatalf("expected empty vault, got %d", len(items))
	}
}
