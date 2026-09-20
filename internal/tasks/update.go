package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/emersion/go-ical"
	"net/url"
	"prism/internal/caldav"
	"strconv"
	"strings"
	"time"
)

// Patch preserves omitted fields; due="" explicitly removes the deadline.
// Shared by the HTTP form and the agent tool.
type Patch struct {
	Title    *string `json:"title,omitempty"`
	Priority *string `json:"priority,omitempty"`
	Due      *string `json:"due,omitempty"`
}

func (p Patch) Validate() error {
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if p.Priority != nil && *p.Priority != "low" && *p.Priority != "normal" && *p.Priority != "high" {
		return fmt.Errorf("priority must be low, normal or high")
	}
	_, err := p.Deadline()
	return err
}
func (p Patch) Deadline() (*time.Time, error) {
	if p.Due == nil || *p.Due == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, *p.Due, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("invalid due date %q; use YYYY-MM-DD or an ISO date/time", *p.Due)
}
func (p *unavailableProvider) Update(context.Context, string, Patch) error { return p.fail() }
func (p *DBProvider) Update(ctx context.Context, id string, patch Patch) error {
	if err := patch.Validate(); err != nil {
		return err
	}
	iid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	due, _ := patch.Deadline()
	return p.Store.PatchTask(ctx, p.Session, iid, patch.Title, patch.Priority, patch.Due != nil, due)
}
func (p *TodoistProvider) Update(ctx context.Context, id string, patch Patch) error {
	if err := patch.Validate(); err != nil {
		return err
	}
	body := map[string]any{}
	if patch.Title != nil {
		body["content"] = *patch.Title
	}
	if patch.Priority != nil {
		body["priority"] = toTodoistPriority(*patch.Priority)
	}
	if patch.Due != nil {
		due, _ := patch.Deadline()
		if due == nil {
			body["due_string"] = "no date"
			body["due_lang"] = "en"
		} else if len(*patch.Due) == 10 {
			body["due_date"] = *patch.Due
		} else {
			body["due_datetime"] = due.UTC().Format(time.RFC3339)
		}
	}
	b, _ := json.Marshal(body)
	res, err := p.do(ctx, "POST", "/tasks/"+url.PathEscape(id), b)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return todoistErr(res)
	}
	return nil
}
func (p *CalDAVProvider) Update(ctx context.Context, id string, patch Patch) error {
	if err := patch.Validate(); err != nil {
		return err
	}
	path, occ := caldav.SplitOccurrenceID(id)
	if occ != "" {
		return caldav.ErrSingleOccurrence("edit")
	}
	conn, err := p.cfg.Connect(ctx)
	if err != nil {
		return err
	}
	if _, err = caldav.ObjectIn(conn.TaskPath, path); err != nil {
		return err
	}
	obj, err := conn.Client.GetCalendarObject(ctx, path)
	if err != nil {
		return err
	}
	if obj == nil || obj.Data == nil {
		return fmt.Errorf("task not found")
	}
	if err = applyTaskPatch(masterTodo(obj.Data), patch); err != nil {
		return err
	}
	_, err = conn.Client.PutCalendarObject(ctx, path, obj.Data)
	return err
}
func applyTaskPatch(todo *ical.Component, patch Patch) error {
	if todo == nil {
		return fmt.Errorf("object has no VTODO")
	}
	if err := patch.Validate(); err != nil {
		return err
	}
	if patch.Title != nil {
		todo.Props.SetText(ical.PropSummary, *patch.Title)
	}
	if patch.Priority != nil {
		todo.Props.SetText(ical.PropPriority, toICalPriority(*patch.Priority))
	}
	if patch.Due != nil {
		due, _ := patch.Deadline()
		if due == nil {
			todo.Props.Del(ical.PropDue)
		} else {
			todo.Props.SetDateTime(ical.PropDue, *due)
			todo.Props.Del(ical.PropDuration)
		}
	}
	return nil
}
