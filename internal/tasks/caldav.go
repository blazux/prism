package tasks

import (
	"context"
	"fmt"
	"strings"
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
	// RFC 5545 gives a VTODO four statuses. Reading only COMPLETED made a
	// cancelled task come back as an ordinary thing to do.
	statusCancelled = "CANCELLED"
	prioHigh        = "1"
	prioNormal      = "5"
	prioLow         = "9"
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
		if obj.Data == nil {
			continue
		}
		out = append(out, itemsFromTodos(obj.Path, obj.ModTime, todosOf(obj.Data), includeDone)...)
	}
	return out, nil
}

// itemsFromTodos turns the VTODO components of ONE object into items. An object
// holds several when a repeating task carries its overridden occurrences next
// to the master; reading only the first hid every one of them.
func itemsFromTodos(objectPath string, modTime time.Time, todos []*ical.Component, includeDone bool) []Item {
	identified := false
	for _, todo := range todos {
		if todo.Props.Get(ical.PropRecurrenceID) != nil {
			identified = true
			break
		}
	}
	out := make([]Item, 0, len(todos))
	for _, todo := range todos {
		title, _ := todo.Props.Text(ical.PropSummary)
		status, _ := todo.Props.Text(ical.PropStatus)
		it := Item{ID: objectPath, Title: title, Priority: fromICalPriority(todo), CreatedAt: modTime}
		it.Done = strings.EqualFold(strings.TrimSpace(status), statusDone)
		it.Cancelled = strings.EqualFold(strings.TrimSpace(status), statusCancelled)
		it.Recurring = todo.Props.Get(ical.PropRecurrenceRule) != nil
		// A cancelled task is no more outstanding than a finished one: keep it
		// out of the default list instead of passing it off as a commitment.
		if (it.Done || it.Cancelled) && !includeDone {
			continue
		}
		var start time.Time
		if due, err := todo.Props.DateTime(ical.PropDue, time.Local); err == nil && !due.IsZero() {
			it.DueAt = &due
			start = due
		}
		occ := caldav.RecurrenceKey(todo, start)
		if occ == "" && len(todos) > 1 && !identified {
			occ = caldav.CompactUTC(start)
		}
		if occ != "" {
			it.ID = objectPath + caldav.OccurrenceSep + occ
			it.Recurring = true
		}
		out = append(out, it)
	}
	return out
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
	objectPath, occurrence := caldav.SplitOccurrenceID(id)
	if occurrence != "" {
		return caldav.ErrSingleOccurrence("complete")
	}
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	if _, err := caldav.ObjectIn(conn.TaskPath, objectPath); err != nil {
		return err
	}
	obj, err := conn.Client.GetCalendarObject(ctx, objectPath)
	if err != nil {
		return err
	}
	todo := masterTodo(obj.Data)
	if todo == nil {
		return fmt.Errorf("object has no VTODO")
	}
	// Completing the master of a repeating task closes the WHOLE series on the
	// user's real task manager: RFC 5545 completes one occurrence by adding an
	// override component carrying its RECURRENCE-ID. Refuse rather than make
	// "bins out, done" delete the weekly reminder for good.
	if done && todo.Props.Get(ical.PropRecurrenceRule) != nil {
		return fmt.Errorf("%q repeats: ticking it here would close the whole series, not today's occurrence. Complete this one in your task app, or delete the series if you are done with it", id)
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
	_, err = conn.Client.PutCalendarObject(ctx, objectPath, obj.Data)
	return err
}

func (p *CalDAVProvider) Delete(ctx context.Context, id string) error {
	objectPath, occurrence := caldav.SplitOccurrenceID(id)
	if occurrence != "" {
		return caldav.ErrSingleOccurrence("delete")
	}
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	target, err := caldav.ObjectIn(conn.TaskPath, objectPath)
	if err != nil {
		return err
	}
	return conn.Client.RemoveAll(ctx, target)
}

// ─── helpers ────────────────────────────────────────────────────────────────────

// todosOf returns every VTODO the object carries, in file order.
func todosOf(cal *ical.Calendar) []*ical.Component {
	if cal == nil {
		return nil
	}
	var out []*ical.Component
	for _, child := range cal.Children {
		if child.Name == ical.CompToDo {
			out = append(out, child)
		}
	}
	return out
}

// masterTodo is the series master: the component with no RECURRENCE-ID, which
// is the one a change to "the task" means.
func masterTodo(cal *ical.Calendar) *ical.Component {
	todos := todosOf(cal)
	for _, todo := range todos {
		if todo.Props.Get(ical.PropRecurrenceID) == nil {
			return todo
		}
	}
	if len(todos) > 0 {
		return todos[0]
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
