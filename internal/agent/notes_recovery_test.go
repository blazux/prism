package agent

import (
	"strings"
	"testing"

	"prism/internal/notes"
)

// Agent edits must retain exactly the same recoverable data as edits in Notes.
// All storage is disposable; no model, personal notes or external service is used.
func TestAgentNoteEditsAndDeletesRemainRecoverable(t *testing.T) {
	for _, kind := range []string{"local", "vault"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			store := resourceDatabase(t)
			executor := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
			executor.SetMemoryStore(store)
			executor.SetPersonalScope("u71")
			if kind == "vault" {
				if err := executor.userStore().SetConfig(ctx, notes.KeyProvider, "vault"); err != nil {
					t.Fatal(err)
				}
				if err := executor.userStore().SetConfig(ctx, notes.KeyVaultPath, t.TempDir()); err != nil {
					t.Fatal(err)
				}
			}
			const original = "# Réseau\nUne procédure déjà vérifiée."
			if _, err := executor.noteTool(ctx, "add", "", map[string]any{"title": "Procédure", "body": original}); err != nil {
				t.Fatal(err)
			}
			provider := notes.ProviderFor(ctx, executor.userStore(), "u71")
			items, err := provider.List(ctx)
			if err != nil || len(items) != 1 {
				t.Fatalf("created note: %+v %v", items, err)
			}
			if _, err := executor.noteTool(ctx, "update", items[0].ID, map[string]any{"body": "Une nouvelle version à conserver."}); err != nil {
				t.Fatal(err)
			}
			recovery := provider.(notes.Recovery)
			versions, err := recovery.History(ctx, items[0].ID, "")
			if err != nil || len(versions) != 1 {
				t.Fatalf("agent edit missing from history: %+v %v", versions, err)
			}
			version, err := recovery.ReadVersion(ctx, items[0].ID, versions[0].ID)
			if err != nil || version.Body != original {
				t.Fatalf("original text lost: %+v %v", version, err)
			}
			out, err := executor.noteTool(ctx, "delete", items[0].ID, nil)
			if err != nil || !strings.Contains(out, "trash") {
				t.Fatalf("delete did not report recovery: %q %v", out, err)
			}
			if active, err := provider.List(ctx); err != nil || len(active) != 0 {
				t.Fatalf("deleted note still active: %+v %v", active, err)
			}
			trash, err := recovery.Trash(ctx)
			if err != nil || len(trash) != 1 || trash[0].Title != "Procédure" || trash[0].DeletedAt == nil {
				t.Fatalf("agent deletion missing from trash: %+v %v", trash, err)
			}
			if _, err := recovery.Restore(ctx, trash[0].ID, ""); err != nil {
				t.Fatal(err)
			}
			restored, err := provider.List(ctx)
			if err != nil || len(restored) != 1 || restored[0].Body != "Une nouvelle version à conserver." {
				t.Fatalf("restore changed the content: %+v %v", restored, err)
			}
		})
	}
}
