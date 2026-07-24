package calendar

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	xcaldav "github.com/emersion/go-webdav/caldav"

	"prism/internal/caldav"
)

type CalDAVProvider struct{ cfg caldav.Config }

func (p *CalDAVProvider) Kind() string { return "caldav" }

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
		evs := obj.Data.Events()
		if len(evs) == 0 {
			continue
		}
		ev := evs[0]
		title, _ := ev.Props.Text(ical.PropSummary)
		desc, _ := ev.Props.Text(ical.PropDescription)
		loc, _ := ev.Props.Text(ical.PropLocation)
		st, _ := ev.DateTimeStart(time.Local)
		it := Item{ID: obj.Path, Title: title, Description: desc, Location: loc, StartAt: st, CreatedAt: obj.ModTime}
		if et, err := ev.DateTimeEnd(time.Local); err == nil && !et.IsZero() {
			it.EndAt = &et
		}
		out = append(out, it)
	}
	return out, nil
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

// Update PUTs a fresh VEVENT to the SAME href Add originally created (id is
// that href, e.g. ".../prism-<nanotime>.ics") — an in-place overwrite, not a
// new object. The UID is recovered from that path (Add's own naming
// convention: ObjectPath = uid + ".ics") so the resource keeps the same
// identity server-side; if id doesn't match that convention (an event this
// provider didn't create), a fresh UID is used — still a same-href PUT, just
// without a UID to preserve.
// uidFromObjectPath recovers the UID Add embedded in the href it returned
// (ObjectPath = uid + ".ics"), or a fresh one if id doesn't match that shape
// (an event this provider didn't create itself).
func uidFromObjectPath(id string) string {
	uid := strings.TrimSuffix(path.Base(id), ".ics")
	if uid == "" || uid == "." || uid == "/" {
		return caldav.NewUID()
	}
	return uid
}

func (p *CalDAVProvider) Update(ctx context.Context, id, title, description, location string, start time.Time, end *time.Time) error {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	uid := uidFromObjectPath(id)
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
	_, err = conn.Client.PutCalendarObject(ctx, id, caldav.WrapCalendar(ev.Component))
	return err
}

func (p *CalDAVProvider) Delete(ctx context.Context, id string) error {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	return conn.Client.RemoveAll(ctx, id)
}
