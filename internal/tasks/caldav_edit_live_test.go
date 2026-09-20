package tasks

import (
	"context"
	"testing"
	"time"
)

// Only point this opt-in test at a disposable Radicale calendar collection.
func TestCalDAVPartialEditLive(t *testing.T) {
	p, _ := liveProvider(t)
	ctx := context.Background()
	due := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	id, err := p.Add(ctx, "Prism test", "high", &due)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Delete(ctx, id) })
	if err = p.Update(ctx, id, Patch{Title: textPtr("Changed")}); err != nil {
		t.Fatal(err)
	}
	assert := func(done bool, hasDue bool) {
		t.Helper()
		items, err := p.List(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.ID == id {
				if it.Title != "Changed" || it.Priority != "high" || it.Done != done || (it.DueAt != nil) != hasDue {
					t.Fatalf("partial edit: %+v", it)
				}
				return
			}
		}
		t.Fatal("task missing")
	}
	assert(false, true)
	if err = p.SetDone(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err = p.Update(ctx, id, Patch{Due: textPtr("")}); err != nil {
		t.Fatal(err)
	}
	assert(true, false)
	if err = p.SetDone(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	assert(false, false)
	if err = p.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	items, err := p.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == id {
			t.Fatal("deleted task still listed")
		}
	}
}
