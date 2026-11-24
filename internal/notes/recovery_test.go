package notes

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestVaultExplicitRenameUpdatesOnlyFrontmatterTitle(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(strconv.Quote(newline), func(t *testing.T) {
			p := &VaultProvider{Dir: t.TempDir()}
			ctx := t.Context()
			id := "2026-11-24.md"
			original := strings.Join([]string{"---", "# Keep this comment", "title: 'Old'", "tags: [réseau]", "custom: {preserve: true}", "description: >-", "  Keep the original layout.", "---", "Été : [[Other]]", ""}, newline)
			if err := os.WriteFile(filepath.Join(p.Dir, id), []byte(original), 0644); err != nil {
				t.Fatal(err)
			}
			// A routine save must not normalize quotes, unknown YAML or line endings.
			if saved, err := p.Save(ctx, id, "Old", original, "réseau"); err != nil || saved != id {
				t.Fatalf("ordinary save: %q %v", saved, err)
			}
			raw, _ := os.ReadFile(filepath.Join(p.Dir, id))
			if string(raw) != original {
				t.Fatal("ordinary save reformatted frontmatter")
			}
			const title = `Nouveau : "réseau"`
			renamed, err := p.Save(ctx, id, title, original, "réseau")
			if err != nil {
				t.Fatal(err)
			}
			expected := strings.Replace(original, "title: 'Old'", "title: "+strconv.Quote(title), 1)
			raw, _ = os.ReadFile(filepath.Join(p.Dir, renamed))
			if string(raw) != expected {
				t.Fatalf("rename changed other raw bytes:\n%s", raw)
			}
			items, err := p.List(ctx)
			if err != nil || len(items) != 1 || items[0].Title != title {
				t.Fatalf("canonical renamed title: %+v %v", items, err)
			}
			aliases := strings.Join(items[0].Aliases, "|")
			if !strings.Contains(aliases, "Old") || !strings.Contains(aliases, id) {
				t.Fatalf("rename aliases missing: %+v", items[0].Aliases)
			}
			versions, err := p.History(ctx, renamed, "")
			if err != nil || len(versions) != 1 {
				t.Fatalf("rename history: %+v %v", versions, err)
			}
			old, err := p.ReadVersion(ctx, renamed, versions[0].ID)
			if err != nil || old.Body != original || old.Title != "Old" {
				t.Fatalf("original snapshot changed: %+v %v", old, err)
			}
			restored, err := p.Restore(ctx, renamed, versions[0].ID)
			if err != nil || restored != id {
				t.Fatalf("restore original id: %q %v", restored, err)
			}
			raw, _ = os.ReadFile(filepath.Join(p.Dir, id))
			if string(raw) != original {
				t.Fatal("restore did not retain original raw frontmatter")
			}
			items, _ = p.List(ctx)
			if len(items) != 1 || items[0].Title != "Old" {
				t.Fatalf("restored title: %+v", items)
			}
		})
	}
}

func TestVaultBodyEditCanChangeFrontmatterTitleWithoutReformatting(t *testing.T) {
	p := &VaultProvider{Dir: t.TempDir()}
	ctx := t.Context()
	original := "---\ntitle: 'Old'\ncustom: unchanged\n---\nBody"
	id, err := p.Save(ctx, "", "Old", original, "")
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(original, "title: 'Old'", `title: "Metadata title"`, 1)
	saved, err := p.Save(ctx, id, "Old", edited, "")
	if err != nil || saved != id {
		t.Fatalf("body save: %q %v", saved, err)
	}
	items, err := p.List(ctx)
	if err != nil || len(items) != 1 || items[0].Body != edited || items[0].Title != "Metadata title" {
		t.Fatalf("body save changed Markdown metadata: %+v %v", items, err)
	}
}

func TestVaultRenameDoesNotFlattenMultilineYAMLTitle(t *testing.T) {
	p := &VaultProvider{Dir: t.TempDir()}
	ctx := t.Context()
	body := "---\ntitle: >-\n  An existing multiline title\n---\nBody"
	id, err := p.Save(ctx, "", "Page", body, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Save(ctx, id, "New", body, ""); err == nil {
		t.Fatal("a flat title replacement damaged multiline YAML")
	}
	raw, _ := os.ReadFile(filepath.Join(p.Dir, id))
	if string(raw) != body {
		t.Fatal("failed rename changed Markdown")
	}
}

func TestVaultRecoveryPreservesRawUnicodeAndVersions(t *testing.T) {
	p := &VaultProvider{Dir: t.TempDir()}
	ctx := t.Context()
	const original = "---\ntags: [réseau]\n---\nÉté : réparer le câble.\n"
	id, err := p.Save(ctx, "", "Réseau", original, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Save(ctx, id, "Réseau", original, "réseau"); err != nil {
		t.Fatal(err)
	}
	versions, err := p.History(ctx, id, "")
	if err != nil || len(versions) != 0 {
		t.Fatalf("unchanged note history: %+v %v", versions, err)
	}
	const changed = "---\ntags: [réseau]\n---\nCâble remplacé.\n"
	if _, err = p.Save(ctx, id, "Réseau", changed, ""); err != nil {
		t.Fatal(err)
	}
	versions, err = p.History(ctx, id, "")
	if err != nil || len(versions) != 1 || versions[0].Body != "" {
		t.Fatalf("history metadata: %+v %v", versions, err)
	}
	v, err := p.ReadVersion(ctx, id, versions[0].ID)
	if err != nil || v.Body != original {
		t.Fatalf("raw snapshot changed: %+v %v", v, err)
	}
	if _, err = p.Restore(ctx, id, v.ID); err != nil {
		t.Fatal(err)
	}
	items, err := p.List(ctx)
	if err != nil || len(items) != 1 || items[0].Body != original {
		t.Fatalf("restored body: %+v %v", items, err)
	}
	versions, _ = p.History(ctx, id, "")
	if len(versions) != 2 {
		t.Fatal("restore did not preserve replaced body")
	}
	if err = p.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if items, _ := p.List(ctx); len(items) != 0 {
		t.Fatal("deleted note remains active")
	}
	trash, err := p.Trash(ctx)
	if err != nil || len(trash) != 1 || trash[0].Body != "" || !strings.HasPrefix(trash[0].ID, "trash:") {
		t.Fatalf("trash metadata: %+v %v", trash, err)
	}
	if err = os.WriteFile(filepath.Join(p.Dir, id), []byte("external file"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Restore(ctx, trash[0].ID, ""); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("occupied restore overwrote file: %v", err)
	}
	os.Remove(filepath.Join(p.Dir, id))
	if restored, err := p.Restore(ctx, trash[0].ID, ""); err != nil || restored != id {
		t.Fatalf("restore: %q %v", restored, err)
	}
	raw, _ := os.ReadFile(filepath.Join(p.Dir, id))
	if string(raw) != original {
		t.Fatal("trash restoration changed frontmatter/Unicode")
	}
}

func TestVaultRecoveryRenamesAndOldPathsStayUnambiguous(t *testing.T) {
	p := &VaultProvider{Dir: t.TempDir()}
	ctx := t.Context()
	id, _ := p.Save(ctx, "", "Old", "original", "")
	renamed, err := p.Save(ctx, id, "New", "new body", "")
	if err != nil || renamed != "New.md" {
		t.Fatalf("rename: %q %v", renamed, err)
	}
	if _, err = p.Save(ctx, id, "New", "changed through old id", ""); err != nil {
		t.Fatal(err)
	}
	versions, err := p.History(ctx, id, "")
	if err != nil || len(versions) != 2 {
		t.Fatalf("old alias history: %+v %v", versions, err)
	}
	newID, err := p.Save(ctx, "", "Old", "fresh", "")
	if err != nil || newID == id {
		t.Fatalf("Prism reused historical alias: %s %v", newID, err)
	}
	if err = os.WriteFile(filepath.Join(p.Dir, id), []byte("external old path"), 0644); err != nil {
		t.Fatal(err)
	}
	if history, err := p.History(ctx, id, ""); err != nil || len(history) != 0 {
		t.Fatalf("external file inherited another note history: %+v %v", history, err)
	}
	if _, err = p.ReadVersion(ctx, id, versions[0].ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external file can read alias history: %v", err)
	}
	if _, err = p.Restore(ctx, renamed, versions[1].ID); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("old version filename collision: %v", err)
	}
	os.Remove(filepath.Join(p.Dir, id))
	if restored, err := p.Restore(ctx, renamed, versions[1].ID); err != nil || restored != id {
		t.Fatalf("restore historical title/path: %s %v", restored, err)
	}
}

func TestVaultRecoveryBlocksSymlinksAndSnapshotFailures(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	p := &VaultProvider{Dir: dir}
	ctx := t.Context()
	out := filepath.Join(outside, "private.md")
	os.WriteFile(out, []byte("PRIVATE"), 0644)
	if err := os.Symlink(out, filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	if list, err := p.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("symlink read: %+v %v", list, err)
	}
	if _, err := p.Save(ctx, "link.md", "link", "overwrite", ""); err == nil {
		t.Fatal("symlink save succeeded")
	}
	if err := p.Delete(ctx, "link.md"); err == nil {
		t.Fatal("symlink delete succeeded")
	}
	id, err := p.Save(ctx, "", "Safe", "intact", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, vaultRecoveryDir), []byte("blocks recovery directory"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Save(ctx, id, "Safe", "replacement", ""); err == nil {
		t.Fatal("save continued after snapshot failed")
	}
	if err := p.Delete(ctx, id); err == nil {
		t.Fatal("delete continued after snapshot failed")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, id))
	if string(raw) != "intact" {
		t.Fatal("failed recovery setup damaged note")
	}
	os.Remove(filepath.Join(dir, vaultRecoveryDir))
	os.Symlink(outside, filepath.Join(dir, vaultRecoveryDir))
	if _, err := p.Save(ctx, id, "Safe", "replacement", ""); err == nil {
		t.Fatal("recovery directory symlink accepted")
	}
}

func TestVaultRecoveryTrashDoesNotReuseNewNoteIdentity(t *testing.T) {
	p := &VaultProvider{Dir: t.TempDir()}
	ctx := t.Context()
	id, _ := p.Save(ctx, "", "Page", "old", "")
	if err := p.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	id2, err := p.Save(ctx, "", "Page", "new", "")
	if err != nil || id2 == id {
		t.Fatalf("reused trash identity: %s %v", id2, err)
	}
	history, err := p.History(ctx, id2, "")
	if err != nil || len(history) != 0 {
		t.Fatalf("new page inherited trash history: %+v %v", history, err)
	}
	trash, _ := p.Trash(ctx)
	if _, err := p.Restore(ctx, trash[0].ID, ""); err != nil {
		t.Fatal(err)
	}
	items, err := p.List(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("restored/new pages not independent: %+v %v", items, err)
	}
}
