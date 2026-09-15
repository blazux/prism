package agent

import (
	"testing"

	"prism/internal/notes"
)

// note action=update rewrites the whole note, so a model that passes only the
// field it wants to change used to blank the rest — and was told "updated".
func TestUpdateKeepsTheFieldsTheCallerDidNotPass(t *testing.T) {
	cur := notes.Item{ID: "Réunion.md", Title: "Réunion", Body: "---\ntags: a\n---\ncorps", Tags: "a"}

	title, body, tags := mergeNoteFields(cur, map[string]any{"title": "Compte rendu"})
	if title != "Compte rendu" {
		t.Errorf("title not applied: %q", title)
	}
	if body != cur.Body || tags != cur.Tags {
		t.Errorf("renaming a note destroyed it: body=%q tags=%q", body, tags)
	}

	// An explicit empty string still clears: that is what the Notes app sends
	// when the box is emptied.
	if _, body, _ = mergeNoteFields(cur, map[string]any{"body": ""}); body != "" {
		t.Errorf("an explicit empty body must clear, got %q", body)
	}

	// Nothing passed at all leaves the note exactly as it was.
	title, body, tags = mergeNoteFields(cur, map[string]any{})
	if title != cur.Title || body != cur.Body || tags != cur.Tags {
		t.Error("an empty update changed the note")
	}

	// A wrong type is not a value: keep what is stored rather than blanking.
	if _, body, _ = mergeNoteFields(cur, map[string]any{"body": nil}); body != cur.Body {
		t.Errorf("a null body blanked the note: %q", body)
	}
}
