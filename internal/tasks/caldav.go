package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/emersion/go-ical"
	xcaldav "github.com/emersion/go-webdav/caldav"

	"prism/internal/caldav"
)

type CalDAVProvider struct{ cfg caldav.Config }

func (p *CalDAVProvider) Kind() string { return "caldav" }

const (
	statusDone = "COMPLETED"
	statusOpen = "NEEDS-ACTION"
	prioHigh   = "1"
	prioNormal = "5"
	prioLow    = "9"
)

func (p *CalDAVProvider) List(ctx context.Context, includeDone bool) ([]Item, error) {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if conn.TaskPath == "" {
		return nil, fmt.Errorf("no calendar collection found for tasks")
	}
	q := &xcaldav.CalendarQuery{
		CompRequest: xcaldav.CalendarCompRequest{
			Name:  "VCALENDAR",
			Comps: []xcaldav.CalendarCompRequest{{Name: "VTODO", AllProps: true}},
		},
		CompFilter: xcaldav.CompFilter{
			Name:  "VCALENDAR",
			Comps: []xcaldav.CompFilter{{Name: "VTODO"}},
		},
	}
	objs, err := conn.Client.QueryCalendar(ctx, conn.TaskPath, q)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(objs))
	for _, obj := range objs {
		todo := firstTodo(obj.Data)
		if todo == nil {
			continue
		}
		title, _ := todo.Props.Text(ical.PropSummary)
		status, _ := todo.Props.Text(ical.PropStatus)
		done := status == statusDone
		if done && !includeDone {
			continue
		}
		it := Item{ID: obj.Path, Title: title, Done: done, Priority: fromICalPriority(todo), CreatedAt: obj.ModTime}
		if due, err := todo.Props.DateTime(ical.PropDue, time.Local); err == nil && !due.IsZero() {
			it.DueAt = &due
		}
		out = append(out, it)
	}
	return out, nil
}

func (p *CalDAVProvider) Add(ctx context.Context, title, priority string, due *time.Time) (string, error) {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return "", err
	}
	if conn.TaskPath == "" {
		return "", fmt.Errorf("no calendar collection found for tasks")
	}
	uid := caldav.NewUID()
	todo := ical.NewComponent(ical.CompToDo)
	todo.Props.SetText(ical.PropUID, uid)
	todo.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	todo.Props.SetText(ical.PropSummary, title)
	todo.Props.SetText(ical.PropStatus, statusOpen)
	todo.Props.SetText(ical.PropPriority, toICalPriority(priority))
	if due != nil {
		todo.Props.SetDateTime(ical.PropDue, *due)
	}
	path := caldav.ObjectPath(conn.TaskPath, uid)
	if _, err := conn.Client.PutCalendarObject(ctx, path, caldav.WrapCalendar(todo)); err != nil {
		return "", err
	}
	return path, nil
}

// SetDone fetches the object, flips its VTODO status, and writes it back.
func (p *CalDAVProvider) SetDone(ctx context.Context, id string, done bool) error {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	obj, err := conn.Client.GetCalendarObject(ctx, id)
	if err != nil {
		return err
	}
	todo := firstTodo(obj.Data)
	if todo == nil {
		return fmt.Errorf("object has no VTODO")
	}
	if done {
		todo.Props.SetText(ical.PropStatus, statusDone)
		todo.Props.SetText(ical.PropPercentComplete, "100")
		todo.Props.SetDateTime(ical.PropCompleted, time.Now().UTC())
	} else {
		todo.Props.SetText(ical.PropStatus, statusOpen)
		todo.Props.Del(ical.PropPercentComplete)
		todo.Props.Del(ical.PropCompleted)
	}
	_, err = conn.Client.PutCalendarObject(ctx, id, obj.Data)
	return err
}

func (p *CalDAVProvider) Delete(ctx context.Context, id string) error {
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	return conn.Client.RemoveAll(ctx, id)
}

// ─── helpers ────────────────────────────────────────────────────────────────────

func firstTodo(cal *ical.Calendar) *ical.Component {
	if cal == nil {
		return nil
	}
	for _, child := range cal.Children {
		if child.Name == ical.CompToDo {
			return child
		}
	}
	return nil
}

func toICalPriority(p string) string {
	switch p {
	case "high":
		return prioHigh
	case "low":
		return prioLow
	default:
		return prioNormal
	}
}

func fromICalPriority(todo *ical.Component) string {
	v, err := todo.Props.Text(ical.PropPriority)
	if err != nil || v == "" {
		return "normal"
	}
	switch {
	case v >= "1" && v <= "4":
		return "high"
	case v >= "6" && v <= "9":
		return "low"
	default:
		return "normal"
	}
}
