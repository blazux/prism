package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"prism/internal/notes"
)

func TestParseTime(t *testing.T) {
	good := []string{"2026-07-01", "2026-07-01 09:30", "2026-07-01T09:30", "2026-07-01T09:30:00Z"}
	for _, s := range good {
		if _, err := parseTime(s); err != nil {
			t.Errorf("parseTime(%q) unexpected error: %v", s, err)
		}
	}
	if _, err := parseTime("not a date"); err == nil {
		t.Error("expected error for invalid time")
	}
	if parseTimePtr("nope") != nil {
		t.Error("parseTimePtr should return nil on bad input")
	}
}

func TestParseIDAndIDArg(t *testing.T) {
	if parseID(" 42 ") != 42 {
		t.Error("parseID should trim and parse")
	}
	if parseID("abc") != 0 {
		t.Error("parseID should be 0 on garbage")
	}
	if got := idArg(map[string]interface{}{"id": float64(7)}); got != "7" {
		t.Errorf("idArg number = %q, want 7", got)
	}
	if got := idArg(map[string]interface{}{"id": "9"}); got != "9" {
		t.Errorf("idArg string = %q, want 9", got)
	}
	if got := idArg(map[string]interface{}{}); got != "" {
		t.Errorf("idArg missing = %q, want empty", got)
	}
}

// fakeNotesProvider lets the lookup guard be tested without a database.
type fakeNotesProvider struct {
	items   []notes.Item
	listErr error
}

func (f *fakeNotesProvider) List(ctx context.Context) ([]notes.Item, error) {
	return f.items, f.listErr
}
func (f *fakeNotesProvider) Save(ctx context.Context, id, title, body, tags string) (string, error) {
	return id, nil
}
func (f *fakeNotesProvider) Delete(ctx context.Context, id string) error { return nil }
func (f *fakeNotesProvider) Kind() string                                { return "local" }

// A wrong id must come back with the ids that DO exist (so the model can
// self-correct instead of re-guessing), a right id must return the item (so
// delete can echo the destroyed content), and a listing failure must not
// block the action.
func TestNoteLookup(t *testing.T) {
	ctx := context.Background()
	prov := &fakeNotesProvider{items: []notes.Item{
		{ID: "2", Title: "Trunk Group BroadSoft", Body: "..."},
		{ID: "5", Title: "urllib timeout", Body: "..."},
	}}

	if item, hint := noteLookup(ctx, prov, "5"); item == nil || hint != "" {
		t.Errorf("existing id: want (item, no hint), got (%v, %q)", item, hint)
	}
	item, hint := noteLookup(ctx, prov, "7")
	if item != nil {
		t.Fatal("unknown id should not resolve to an item")
	}
	if !strings.Contains(hint, "2") || !strings.Contains(hint, "Trunk Group BroadSoft") || !strings.Contains(hint, "5") {
		t.Errorf("hint should list existing ids and titles, got %q", hint)
	}

	if item, hint := noteLookup(ctx, &fakeNotesProvider{listErr: errors.New("boom")}, "5"); item != nil || hint != "" {
		t.Errorf("list failure must not guard (nil, empty), got (%v, %q)", item, hint)
	}

	if _, hint := noteLookup(ctx, &fakeNotesProvider{}, "1"); !strings.Contains(hint, "no notes") {
		t.Errorf("empty store hint should say there are no notes, got %q", hint)
	}
}

func TestPimHint(t *testing.T) {
	if got := pimHint("notes", nil, nil); !strings.Contains(got, "no notes") {
		t.Errorf("empty hint = %q", got)
	}
	got := pimHint("tasks", []string{"1"}, []string{"Backup"})
	if !strings.Contains(got, `1 ("Backup")`) || !strings.Contains(got, "verbatim") {
		t.Errorf("hint = %q", got)
	}
}
