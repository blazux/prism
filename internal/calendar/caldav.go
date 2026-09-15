package calendar

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	xcaldav "github.com/emersion/go-webdav/caldav"

	"prism/internal/caldav"
)

type CalDAVProvider struct{ cfg caldav.Config }

func (p *CalDAVProvider) Kind() string { return "caldav" }

// occurrenceSep separates an object href from the RECURRENCE-ID of ONE instance
// of a recurring series: "/cal/weekly.ics#20260302T090000Z". A plain href
// addresses the whole object, a suffixed id addresses a single occurrence —
// and mutating one is refused, see errSingleOccurrence.
const occurrenceSep = "#"

// occurrenceKeyRe is the exact shape produced by compactUTC. Requiring it means
// an href that genuinely contains "#" is still addressed whole, unless what
// follows its last "#" happens to look like a UTC timestamp.
var occurrenceKeyRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

func (p *CalDAVProvider) List(ctx context.Context, from, to *time.Time) ([]Item, error) {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if conn.EventPath == "" {
		return nil, fmt.Errorf("no calendar collection found for events")
	}
	start := time.Now().AddDate(-1, 0, 0)
	end := time.Now().AddDate(1, 0, 0)
	if from != nil {
		start = *from
	}
	if to != nil {
		end = *to
	}
	q := &xcaldav.CalendarQuery{
		CompRequest: xcaldav.CalendarCompRequest{
			Name:  "VCALENDAR",
			Comps: []xcaldav.CalendarCompRequest{{Name: "VEVENT", AllProps: true}},
			// Make the server develop the recurrence set over the window. The
			// time-range filter below already MATCHES a repeating event, but
			// without expand the server returns the stored component, whose
			// DTSTART is the start of the SERIES: "what do I have next week"
			// answered with a date months in the past, once, however many
			// occurrences actually fall in the window. Servers that ignore
			// expand still return the master; those items come back flagged
			// Recurring rather than passing for a real appointment.
			Expand: &xcaldav.CalendarExpandRequest{Start: start, End: end},
		},
		CompFilter: xcaldav.CompFilter{
			Name:  "VCALENDAR",
			Comps: []xcaldav.CompFilter{{Name: "VEVENT", Start: start, End: end}},
		},
	}
	objs, err := conn.Client.QueryCalendar(ctx, conn.EventPath, q)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(objs))
	for _, obj := range objs {
		if obj.Data == nil {
			continue
		}
		out = append(out, itemsFromEvents(obj.Path, obj.ModTime, obj.Data.Events())...)
	}
	return out, nil
}

// itemsFromEvents turns the VEVENT components of ONE calendar object into
// items. An object legitimately holds several: the occurrences an expanding
// server returned, or a series master followed by its overridden occurrences
// (each carrying a RECURRENCE-ID). Reading only the first one hid every moved
// occurrence — "Monday 9am, except the 3rd at 2pm" displayed 9am.
func itemsFromEvents(objectPath string, modTime time.Time, evs []ical.Event) []Item {
	// A bundle that carries at least one RECURRENCE-ID has a master: the
	// component without one IS the series, and stays addressable so the series
	// can still be edited or deleted. Only when nothing identifies the
	// instances — an expanding server that omitted RECURRENCE-ID — does
	// position have to stand in, for all of them.
	identified := false
	for i := range evs {
		if evs[i].Props.Get(ical.PropRecurrenceID) != nil {
			identified = true
			break
		}
	}
	out := make([]Item, 0, len(evs))
	for i := range evs {
		ev := evs[i]
		title, _ := ev.Props.Text(ical.PropSummary)
		desc, _ := ev.Props.Text(ical.PropDescription)
		loc, _ := ev.Props.Text(ical.PropLocation)
		st, _ := ev.DateTimeStart(time.Local)
		it := Item{ID: objectPath, Title: title, Description: desc, Location: loc, StartAt: st, CreatedAt: modTime}
		if et, err := ev.DateTimeEnd(time.Local); err == nil && !et.IsZero() {
			it.EndAt = &et
		}
		transp, _ := ev.Props.Text(ical.PropTransparency)
		status, _ := ev.Props.Text(ical.PropStatus)
		it.Free = strings.EqualFold(strings.TrimSpace(transp), "TRANSPARENT")
		it.Cancelled = strings.EqualFold(strings.TrimSpace(status), "CANCELLED")
		it.Recurring = ev.Props.Get(ical.PropRecurrenceRule) != nil

		// Address a single instance by its RECURRENCE-ID so no mutation can be
		// aimed at it: the href it shares with its siblings designates the
		// whole object.
		occ := recurrenceKey(ev, st)
		if occ == "" && len(evs) > 1 && !identified {
			occ = compactUTC(st)
		}
		if occ != "" {
			it.ID = objectPath + occurrenceSep + occ
			it.Recurring = true
		}
		out = append(out, it)
	}
	return out
}

// recurrenceKey identifies the instance an overriding component replaces. An
// unparsable RECURRENCE-ID still marks the component as an instance: fall back
// to its own start, which distinguishes it just as well.
func recurrenceKey(ev ical.Event, start time.Time) string {
	if ev.Props.Get(ical.PropRecurrenceID) == nil {
		return ""
	}
	if t, err := ev.Props.DateTime(ical.PropRecurrenceID, time.UTC); err == nil && !t.IsZero() {
		return compactUTC(t)
	}
	return compactUTC(start)
}

func compactUTC(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// splitOccurrenceID separates the object href from the occurrence key. Only the
// last separator counts, and the suffix must have the exact compactUTC shape.
func splitOccurrenceID(id string) (objectPath, occurrence string) {
	i := strings.LastIndex(id, occurrenceSep)
	if i <= 0 || !occurrenceKeyRe.MatchString(id[i+1:]) {
		return id, ""
	}
	return id[:i], id[i+1:]
}

// errSingleOccurrence refuses to touch one instance of a series. Deleting it
// would remove the object, so the whole series; editing it would mean writing
// an EXDATE or an override component. Refusing out loud beats destroying a
// series on the user's real calendar without saying so.
func errSingleOccurrence(verb string) error {
	return fmt.Errorf("this is a single occurrence of a recurring event and cannot be %sd on its own; "+
		"use the id without its \"%s<date>\" suffix to %s the whole series, or change this one occurrence in your calendar app",
		verb, occurrenceSep, verb)
}

func (p *CalDAVProvider) Add(ctx context.Context, title, description, location string, start time.Time, end *time.Time) (string, error) {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return "", err
	}
	if conn.EventPath == "" {
		return "", fmt.Errorf("no calendar collection found for events")
	}
	uid := caldav.NewUID()
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	ev.Props.SetText(ical.PropSummary, title)
	if description != "" {
		ev.Props.SetText(ical.PropDescription, description)
	}
	if location != "" {
		ev.Props.SetText(ical.PropLocation, location)
	}
	ev.Props.SetDateTime(ical.PropDateTimeStart, start)
	if end != nil {
		ev.Props.SetDateTime(ical.PropDateTimeEnd, *end)
	}
	path := caldav.ObjectPath(conn.EventPath, uid)
	if _, err := conn.Client.PutCalendarObject(ctx, path, caldav.WrapCalendar(ev.Component)); err != nil {
		return "", err
	}
	return path, nil
}

// Update edits the object at the SAME href in place (id is that href, as
// returned by Add or List). It is a read-modify-write: a PUT replaces the whole
// resource, so rebuilding a bare VEVENT out of the five edited fields — what
// this used to do — silently dropped the recurrence rule, every overridden
// occurrence, the attendees and the alarms. A weekly meeting became a one-off.
func (p *CalDAVProvider) Update(ctx context.Context, id, title, description, location string, start time.Time, end *time.Time) error {
	objectPath, occurrence := splitOccurrenceID(id)
	if occurrence != "" {
		return errSingleOccurrence("edit")
	}
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	obj, err := conn.Client.GetCalendarObject(ctx, objectPath)
	if err != nil {
		// Never fall back to overwriting with a fresh event: a transient read
		// failure would then destroy exactly what this read protects.
		return fmt.Errorf("cannot read the event before editing it: %w", err)
	}
	if obj == nil || obj.Data == nil {
		return fmt.Errorf("no event found at %s", objectPath)
	}
	if err := applyEventEdit(obj.Data, title, description, location, start, end); err != nil {
		return err
	}
	_, err = conn.Client.PutCalendarObject(ctx, objectPath, obj.Data)
	return err
}

// applyEventEdit changes in place only the fields the edit form owns, on the
// series master (the component with no RECURRENCE-ID). Everything else the
// object carries is left exactly as the server sent it.
func applyEventEdit(cal *ical.Calendar, title, description, location string, start time.Time, end *time.Time) error {
	evs := cal.Events()
	var master *ical.Component
	for i := range evs {
		if evs[i].Props.Get(ical.PropRecurrenceID) == nil {
			master = evs[i].Component
			break
		}
		if master == nil {
			master = evs[i].Component // only overrides: editing the first beats refusing
		}
	}
	if master == nil {
		return fmt.Errorf("calendar object carries no event")
	}
	master.Props.SetText(ical.PropSummary, title)
	setOrDelete(master.Props, ical.PropDescription, description)
	setOrDelete(master.Props, ical.PropLocation, location)
	master.Props.SetDateTime(ical.PropDateTimeStart, start)
	if end != nil {
		master.Props.SetDateTime(ical.PropDateTimeEnd, *end)
		// DTEND and DURATION are mutually exclusive; keeping both would make
		// the event invalid for the server.
		master.Props.Del(ical.PropDuration)
	} else if master.Props.Get(ical.PropDuration) == nil {
		master.Props.Del(ical.PropDateTimeEnd)
	}
	master.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	return nil
}

func setOrDelete(props ical.Props, name, value string) {
	if value == "" {
		props.Del(name)
		return
	}
	props.SetText(name, value)
}

func (p *CalDAVProvider) Delete(ctx context.Context, id string) error {
	objectPath, occurrence := splitOccurrenceID(id)
	if occurrence != "" {
		return errSingleOccurrence("delete")
	}
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	return conn.Client.RemoveAll(ctx, objectPath)
}
