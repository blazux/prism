package tasks

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"prism/internal/caldav"
)

func decodeTodos(t *testing.T, raw string) []*ical.Component {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return todosOf(cal)
}

const weeklyBins = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VTODO
UID:bins
DTSTAMP:20260101T000000Z
DUE:20260106T080000Z
SUMMARY:Sortir les poubelles
RRULE:FREQ=WEEKLY
END:VTODO
BEGIN:VTODO
UID:bins
RECURRENCE-ID:20260113T080000Z
DTSTAMP:20260101T000000Z
DUE:20260113T200000Z
SUMMARY:Sortir les poubelles (reporte)
END:VTODO
END:VCALENDAR
`

// A cancelled task holds no commitment. Reading only COMPLETED made it come
// back as an ordinary thing to do, counted in the open tasks.
func TestCancelledTaskIsNotSomethingToDo(t *testing.T) {
	const raw = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VTODO
UID:x
DTSTAMP:20260101T000000Z
SUMMARY:Reserver la salle
STATUS:CANCELLED
END:VTODO
END:VCALENDAR
`
	todos := decodeTodos(t, raw)
	if got := itemsFromTodos("/t/x.ics", time.Time{}, todos, false); len(got) != 0 {
		t.Fatalf("a cancelled task was listed as open: %+v", got)
	}
	got := itemsFromTodos("/t/x.ics", time.Time{}, todos, true)
	if len(got) != 1 || !got[0].Cancelled || got[0].Done {
		t.Fatalf("got %+v", got)
	}
}

// The postponed occurrence lived in the same object as the master and was
// invisible, exactly as a moved event used to be.
func TestEveryTodoOfAnObjectIsRead(t *testing.T) {
	items := itemsFromTodos("/t/bins.ics", time.Time{}, decodeTodos(t, weeklyBins), false)
	if len(items) != 2 {
		t.Fatalf("got %d items, want the series and its postponed occurrence", len(items))
	}
	if items[0].ID != "/t/bins.ics" || !items[0].Recurring {
		t.Errorf("series master: id=%q recurring=%v", items[0].ID, items[0].Recurring)
	}
	moved := items[1]
	if moved.ID != "/t/bins.ics"+caldav.OccurrenceSep+"20260113T080000Z" {
		t.Errorf("occurrence id %q does not carry its RECURRENCE-ID", moved.ID)
	}
	if moved.DueAt == nil || moved.DueAt.UTC().Hour() != 20 {
		t.Errorf("the postponed due date was lost: %v", moved.DueAt)
	}
}

// Ticking one occurrence must not close the weekly reminder for good.
func TestCompletingARepeatingTaskIsRefused(t *testing.T) {
	p := &CalDAVProvider{}
	err := p.SetDone(t.Context(), "/t/bins.ics"+caldav.OccurrenceSep+"20260113T080000Z", true)
	if err == nil || !strings.Contains(err.Error(), "single occurrence") {
		t.Fatalf("occurrence: %v", err)
	}
	err = p.Delete(t.Context(), "/t/bins.ics"+caldav.OccurrenceSep+"20260113T080000Z")
	if err == nil || !strings.Contains(err.Error(), "single occurrence") {
		t.Fatalf("delete: %v", err)
	}
}

// masterTodo picks the series, never one of its overrides.
func TestMasterIsTheComponentWithoutARecurrenceID(t *testing.T) {
	cal, err := ical.NewDecoder(strings.NewReader(weeklyBins)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	master := masterTodo(cal)
	if master == nil || master.Props.Get(ical.PropRecurrenceRule) == nil {
		t.Fatal("master not found")
	}
	if master.Props.Get(ical.PropRecurrenceID) != nil {
		t.Fatal("an override was taken for the series")
	}
}
