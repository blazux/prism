package calendar

import (
	"context"
	"testing"
	"time"
)

// Only point this opt-in test at a disposable Radicale calendar collection.
func TestCalDAVPartialEditLive(t *testing.T) {
	p, _ := liveProvider(t)
	ctx := context.Background()
	start := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	id, err := p.Add(ctx, "Prism test", "Keep description", "Room A", start, &end, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Delete(ctx, id) })
	if err = Update(ctx, p, id, Patch{Start: ptr("2026-09-23T11:00:00Z"), Location: ptr("Room B")}); err != nil {
		t.Fatal(err)
	}
	ev, err := Get(ctx, p, id)
	if err != nil || ev.Description != "Keep description" || ev.Location != "Room B" || ev.EndAt == nil || ev.EndAt.Sub(ev.StartAt) != 90*time.Minute {
		t.Fatalf("partial update: %+v %v", ev, err)
	}
	flag := true
	if err = Update(ctx, p, id, Patch{AllDay: &flag, Start: ptr("2026-09-24"), End: ptr("2026-09-26")}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	items, err := p.List(ctx, &from, &to)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.ID == id {
			found = true
			if !it.AllDay || it.EndAt == nil || it.EndAt.Format("2006-01-02") != "2026-09-26" {
				t.Fatalf("all day: %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("ongoing all-day event missing from overlapping day")
	}
	if err = p.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = Get(ctx, p, id); err == nil {
		t.Fatal("deleted event still readable")
	}
}
