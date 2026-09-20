package calendar

import (
	"github.com/emersion/go-ical"
	"strings"
	"testing"
	"time"
)

func ptr(s string) *string { return &s }
func TestPatchMovesWithoutLosingDurationOrOtherFields(t *testing.T) {
	start := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	cur := Item{Title: "Review", Description: "Keep me", Location: "Room A", StartAt: start, EndAt: &end}
	next, err := (Patch{Start: ptr("2026-09-21T15:00:00Z")}).Merge(cur)
	if err != nil {
		t.Fatal(err)
	}
	if next.Description != cur.Description || next.Location != cur.Location || next.EndAt.Sub(next.StartAt) != 90*time.Minute {
		t.Fatalf("lost fields: %+v", next)
	}
	next, err = (Patch{Description: ptr(""), End: ptr("")}).Merge(cur)
	if err != nil || next.Description != "" || next.EndAt != nil {
		t.Fatalf("clear: %+v %v", next, err)
	}
	for _, patch := range []Patch{{End: ptr("bad")}, {End: ptr("2026-09-19")}, {Title: ptr("")}} {
		if _, err = patch.Merge(cur); err == nil {
			t.Fatal("invalid edit accepted")
		}
	}
}
func TestAllDayUsesCalendarDates(t *testing.T) {
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.FixedZone("local", -4*3600))
	flag := true
	ev, err := (Patch{AllDay: &flag}).Merge(Item{Title: "Holiday", StartAt: start})
	if err != nil {
		t.Fatal(err)
	}
	if ev.EndAt.Sub(ev.StartAt) != 24*time.Hour {
		t.Fatal(ev)
	}
	g := gcalEvent{}
	setGoogleAllDay(&g, start, *ev.EndAt, []bool{true})
	if g.Start.Date != "2026-09-20" || g.Start.DateTime != "" || g.End.Date != "2026-09-21" {
		t.Fatal(g)
	}
	m := msEvent{}
	setMicrosoftAllDay(&m, start, *ev.EndAt, []bool{true})
	if !m.AllDay || !strings.HasSuffix(m.Start.DateTime, "T00:00:00") {
		t.Fatal(m)
	}
	cal := ical.NewCalendar()
	e := ical.NewEvent()
	e.Props.SetText(ical.PropUID, "test")
	cal.Children = append(cal.Children, e.Component)
	if err := applyEventEdit(cal, "Holiday", "", "", start, ev.EndAt, true); err != nil {
		t.Fatal(err)
	}
	if e.Props.Get(ical.PropDateTimeStart).ValueType() != ical.ValueDate {
		t.Fatal("all day stored as instant")
	}
}
