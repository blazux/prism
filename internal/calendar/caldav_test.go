package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

// A weekly series, one occurrence moved to another hour, an alarm and an
// attendee — the shape a real server returns for "the Monday meeting, except
// on 2 March".
const seriesICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:weekly-1
DTSTAMP:20260101T000000Z
DTSTART:20260105T090000Z
DTEND:20260105T100000Z
SUMMARY:Reunion hebdo
LOCATION:Salle A
ATTENDEE:mailto:alice@example.invalid
RRULE:FREQ=WEEKLY
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT15M
DESCRIPTION:Rappel
END:VALARM
END:VEVENT
BEGIN:VEVENT
UID:weekly-1
RECURRENCE-ID:20260302T090000Z
DTSTAMP:20260101T000000Z
DTSTART:20260302T140000Z
DTEND:20260302T150000Z
SUMMARY:Reunion hebdo (decalee)
END:VEVENT
END:VCALENDAR
`

func decodeICS(t *testing.T, raw string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

// The moved occurrence used to be invisible: List kept evs[0] only.
func TestEveryComponentOfAnObjectIsRead(t *testing.T) {
	cal := decodeICS(t, seriesICS)
	items := itemsFromEvents("/cal/weekly-1.ics", time.Time{}, cal.Events())
	if len(items) != 2 {
		t.Fatalf("got %d items, want the master and its moved occurrence", len(items))
	}
	if !items[0].Recurring || items[0].ID != "/cal/weekly-1.ics" {
		t.Errorf("series master: recurring=%v id=%q", items[0].Recurring, items[0].ID)
	}
	moved := items[1]
	if !moved.Recurring {
		t.Error("moved occurrence not flagged as recurring")
	}
	if moved.ID != "/cal/weekly-1.ics"+occurrenceSep+"20260302T090000Z" {
		t.Errorf("occurrence id %q does not carry its RECURRENCE-ID", moved.ID)
	}
	if got := moved.StartAt.UTC().Format(time.RFC3339); got != "2026-03-02T14:00:00Z" {
		t.Errorf("moved occurrence starts at %s, want the overridden hour", got)
	}
}

// An expanding server returns instances sharing one href. None of them may be
// addressable as the object itself, or deleting one would drop the series.
func TestExpandedInstancesAreNeverAddressedAsTheObject(t *testing.T) {
	const expanded = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:weekly-1
DTSTAMP:20260101T000000Z
DTSTART:20260302T090000Z
DTEND:20260302T100000Z
SUMMARY:Reunion hebdo
END:VEVENT
BEGIN:VEVENT
UID:weekly-1
DTSTAMP:20260101T000000Z
DTSTART:20260309T090000Z
DTEND:20260309T100000Z
SUMMARY:Reunion hebdo
END:VEVENT
END:VCALENDAR
`
	items := itemsFromEvents("/cal/weekly-1.ics", time.Time{}, decodeICS(t, expanded).Events())
	if len(items) != 2 {
		t.Fatalf("got %d instances", len(items))
	}
	for _, it := range items {
		if _, occ := splitOccurrenceID(it.ID); occ == "" {
			t.Errorf("instance %q is addressable as the whole object", it.ID)
		}
		if !it.Recurring {
			t.Errorf("instance %q not flagged recurring", it.ID)
		}
	}
	if items[0].ID == items[1].ID {
		t.Error("two instances share one id")
	}
}

// A lone event keeps a plain href: editing and deleting it stay possible.
func TestPlainEventStaysAddressable(t *testing.T) {
	const single = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:one
DTSTAMP:20260101T000000Z
DTSTART:20260105T090000Z
DTEND:20260105T100000Z
SUMMARY:Dentiste
TRANSP:TRANSPARENT
STATUS:CANCELLED
END:VEVENT
END:VCALENDAR
`
	items := itemsFromEvents("/cal/one.ics", time.Time{}, decodeICS(t, single).Events())
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	it := items[0]
	if it.ID != "/cal/one.ics" || it.Recurring {
		t.Errorf("id=%q recurring=%v", it.ID, it.Recurring)
	}
	if !it.Free || !it.Cancelled {
		t.Errorf("TRANSP/STATUS ignored: free=%v cancelled=%v", it.Free, it.Cancelled)
	}
}

func TestSplitOccurrenceID(t *testing.T) {
	for id, want := range map[string]string{
		"/cal/a.ics":                       "",
		"/cal/a.ics#20260302T090000Z":      "20260302T090000Z",
		"/cal/we#ird.ics":                  "",                 // a literal # in the href
		"/cal/we#ird.ics#20260302T090000Z": "20260302T090000Z", // only the last one counts
		"/cal/a.ics#":                      "",
		"/cal/a.ics#not-a-date":            "",
		"#20260302T090000Z":                "", // no object path
	} {
		p, occ := splitOccurrenceID(id)
		if occ != want {
			t.Errorf("%q: occurrence %q, want %q", id, occ, want)
		}
		if want == "" && p != id {
			t.Errorf("%q: object path mangled to %q", id, p)
		}
	}
}

// Mutating one occurrence is refused before any network call, so a provider
// with no connection is enough to prove nothing reaches the server.
func TestMutatingOneOccurrenceIsRefused(t *testing.T) {
	p := &CalDAVProvider{}
	id := "/cal/weekly-1.ics" + occurrenceSep + "20260302T090000Z"
	err := p.Delete(t.Context(), id)
	if err == nil || !strings.Contains(err.Error(), "single occurrence") {
		t.Fatalf("delete: %v", err)
	}
	err = p.Update(t.Context(), id, "x", "", "", time.Now(), nil)
	if err == nil || !strings.Contains(err.Error(), "single occurrence") {
		t.Fatalf("update: %v", err)
	}
}

// The old Update rebuilt a bare VEVENT and PUT it over the object, which
// dropped the rule, the overrides, the alarm and the attendees.
func TestEditingAnEventKeepsEverythingElse(t *testing.T) {
	cal := decodeICS(t, seriesICS)
	start := time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	if err := applyEventEdit(cal, "Reunion equipe", "", "Salle B", start, &end); err != nil {
		t.Fatal(err)
	}
	evs := cal.Events()
	if len(evs) != 2 {
		t.Fatalf("the overridden occurrence was dropped: %d components left", len(evs))
	}
	master := evs[0]
	if master.Props.Get(ical.PropRecurrenceRule) == nil {
		t.Error("the recurrence rule was lost: the series became a one-off")
	}
	if master.Props.Get("ATTENDEE") == nil {
		t.Error("attendees were lost")
	}
	if len(master.Children) != 1 || master.Children[0].Name != ical.CompAlarm {
		t.Error("the alarm was lost")
	}
	if uid, _ := master.Props.Text(ical.PropUID); uid != "weekly-1" {
		t.Errorf("UID changed to %q", uid)
	}
	if summary, _ := master.Props.Text(ical.PropSummary); summary != "Reunion equipe" {
		t.Errorf("summary not applied: %q", summary)
	}
	if loc, _ := master.Props.Text(ical.PropLocation); loc != "Salle B" {
		t.Errorf("location not applied: %q", loc)
	}
	// An empty field from the form clears the property instead of keeping the
	// old value.
	if master.Props.Get(ical.PropDescription) != nil {
		t.Error("cleared description kept its old value")
	}
	if got, _ := master.Props.DateTime(ical.PropDateTimeStart, time.UTC); !got.Equal(start) {
		t.Errorf("start not applied: %s", got)
	}
	// The moved occurrence keeps its own hour: the edit targets the series.
	if got, _ := evs[1].Props.DateTime(ical.PropDateTimeStart, time.UTC); got.Hour() != 14 {
		t.Errorf("the override was rewritten: %s", got)
	}
}

// DTEND and DURATION are mutually exclusive.
func TestEditReplacesDurationWithAnEnd(t *testing.T) {
	const withDuration = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:dur
DTSTAMP:20260101T000000Z
DTSTART:20260105T090000Z
DURATION:PT1H
SUMMARY:Point
END:VEVENT
END:VCALENDAR
`
	cal := decodeICS(t, withDuration)
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err := applyEventEdit(cal, "Point", "", "", start, &end); err != nil {
		t.Fatal(err)
	}
	ev := cal.Events()[0]
	if ev.Props.Get(ical.PropDuration) != nil || ev.Props.Get(ical.PropDateTimeEnd) == nil {
		t.Error("DTEND and DURATION must not coexist")
	}
}
