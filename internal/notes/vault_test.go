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

// A note whose front-matter carries a title used to be renamed by a plain save:
// List reported the front-matter title, Save compared it to the FILE name,
// found them different and renamed the file, killing every [[wikilink]] to it.
func TestSavingAnUnchangedNoteDoesNotRenameIt(t *testing.T) {
	dir := t.TempDir()
	p := &VaultProvider{Dir: dir}
	ctx := context.Background()
	const raw = "---\ntitle: Réunion Digicel\ntags: work\n---\nvoir [[2026-01-05]]"
	if err := os.WriteFile(filepath.Join(dir, "2026-01-05.md"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	items, err := p.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list err=%v n=%d", err, len(items))
	}
	if items[0].Title != "Réunion Digicel" {
		t.Fatalf("title = %q", items[0].Title)
	}

	// Exactly what the app sends back on blur: the values it just read.
	id, err := p.Save(ctx, items[0].ID, items[0].Title, items[0].Body, items[0].Tags)
	if err != nil {
		t.Fatal(err)
	}
	if id != "2026-01-05.md" {
		t.Fatalf("an unchanged save renamed the note to %q", id)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-01-05.md")); err != nil {
		t.Fatalf("original file gone: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "2026-01-05.md")); string(got) != raw {
		t.Fatalf("content changed: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("vault now holds %d files", len(entries))
	}

	// Renaming on purpose still works, and still goes by the displayed title.
	id, err = p.Save(ctx, id, "Compte rendu", raw, "work")
	if err != nil {
		t.Fatal(err)
	}
	if id != "Compte rendu.md" {
		t.Fatalf("deliberate rename did not happen: %q", id)
	}
}

// A write must never leave a half-written note in a vault that is usually
// synchronised elsewhere.
func TestVaultWritesAreAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "replacement" {
		t.Fatalf("content = %q err = %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary file left behind: %d entries", len(entries))
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0644 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

// The tags argument used to be accepted and ignored: the app's tag box emptied
// itself on the next load and the agent was told the change had been made.
func TestVaultRefusesTagsItCannotStore(t *testing.T) {
	dir := t.TempDir()
	p := &VaultProvider{Dir: dir}
	ctx := context.Background()
	const raw = "---\ntags: a, b\n---\ncorps"
	if err := os.WriteFile(filepath.Join(dir, "N.md"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	// Tags that the body does not carry cannot be honoured.
	if _, err := p.Save(ctx, "N.md", "N", raw, "urgent"); err == nil {
		t.Fatal("a tag change the vault cannot store was reported as done")
	} else if !strings.Contains(err.Error(), "front-matter") {
		t.Fatalf("unhelpful error: %v", err)
	}
	// Sending back what was read stays a normal save.
	if _, err := p.Save(ctx, "N.md", "N", raw, "a, b"); err != nil {
		t.Fatalf("round-trip refused: %v", err)
	}
	// And so does a save that does not mention tags at all.
	if _, err := p.Save(ctx, "N.md", "N", raw, ""); err != nil {
		t.Fatalf("tagless save refused: %v", err)
	}
}
