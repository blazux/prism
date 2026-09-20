package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"prism/internal/caldav"
	"strings"
	"time"
)

// Patch is shared by the edit form and agent. Omitted fields survive;
// moving only start preserves the event's duration.
type Patch struct {
	AllDay      *bool   `json:"all_day,omitempty"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Location    *string `json:"location,omitempty"`
	Start       *string `json:"start,omitempty"`
	End         *string `json:"end,omitempty"`
}

func ParseTime(s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("invalid date %q; use YYYY-MM-DD or an ISO date/time", s)
}
func (p Patch) Merge(cur Item) (Item, error) {
	if p.AllDay != nil {
		cur.AllDay = *p.AllDay
	}
	if p.Title != nil {
		cur.Title = strings.TrimSpace(*p.Title)
	}
	if p.Description != nil {
		cur.Description = *p.Description
	}
	if p.Location != nil {
		cur.Location = *p.Location
	}
	if p.Start != nil {
		st, err := ParseTime(*p.Start)
		if err != nil {
			return cur, err
		}
		if st == nil {
			return cur, fmt.Errorf("start is required")
		}
		if p.End == nil && cur.EndAt != nil {
			end := st.Add(cur.EndAt.Sub(cur.StartAt))
			cur.EndAt = &end
		}
		cur.StartAt = *st
	}
	if p.End != nil {
		end, err := ParseTime(*p.End)
		if err != nil {
			return cur, err
		}
		cur.EndAt = end
	}
	if cur.AllDay {
		st := cur.StartAt
		cur.StartAt = time.Date(st.Year(), st.Month(), st.Day(), 0, 0, 0, 0, st.Location())
		if cur.EndAt != nil {
			et := *cur.EndAt
			et = time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, st.Location())
			if !et.After(cur.StartAt) && p.End == nil {
				et = cur.StartAt.AddDate(0, 0, 1)
			}
			cur.EndAt = &et
		}
		if cur.EndAt == nil {
			et := cur.StartAt.AddDate(0, 0, 1)
			cur.EndAt = &et
		}
	}
	if cur.Title == "" {
		return cur, fmt.Errorf("title is required")
	}
	if cur.StartAt.IsZero() {
		return cur, fmt.Errorf("start is required")
	}
	if cur.EndAt != nil && !cur.EndAt.After(cur.StartAt) {
		return cur, fmt.Errorf("end must be after start")
	}
	return cur, nil
}
func Get(ctx context.Context, prov Provider, id string) (Item, error) {
	if strings.TrimSpace(id) == "" {
		return Item{}, fmt.Errorf("event id is required")
	}
	switch p := prov.(type) {
	case *GoogleProvider:
		var e gcalEvent
		err := p.doJSON(ctx, "GET", gcalEventsURL+"/"+url.PathEscape(id), nil, &e)
		return e.item(), err
	case *MicrosoftProvider:
		var e msEvent
		err := p.doJSON(ctx, "GET", graphBase+"/me/events/"+url.PathEscape(id), nil, &e)
		return e.item(), err
	case *CalDAVProvider:
		path, occ := caldav.SplitOccurrenceID(id)
		if occ != "" {
			return Item{}, caldav.ErrSingleOccurrence("edit")
		}
		conn, err := p.cfg.Connect(ctx)
		if err != nil {
			return Item{}, err
		}
		if _, err = caldav.ObjectIn(conn.EventPath, path); err != nil {
			return Item{}, err
		}
		obj, err := conn.Client.GetCalendarObject(ctx, path)
		if err != nil {
			return Item{}, err
		}
		if obj != nil && obj.Data != nil {
			for _, it := range itemsFromEvents(path, obj.ModTime, obj.Data.Events()) {
				if it.ID == id {
					return it, nil
				}
			}
		}
	default:
		items, err := prov.List(ctx, nil, nil)
		if err != nil {
			return Item{}, err
		}
		for _, it := range items {
			if it.ID == id {
				return it, nil
			}
		}
	}
	return Item{}, fmt.Errorf("event %q not found; use calendar list for its id", id)
}
func Update(ctx context.Context, prov Provider, id string, patch Patch) error {
	cur, err := Get(ctx, prov, id)
	if err != nil {
		return err
	}
	next, err := patch.Merge(cur)
	if err != nil {
		return err
	}
	// Cloud PATCHes send only requested fields. In particular, an Outlook
	// location edit must not rewrite an HTML description as plain text.
	switch p := prov.(type) {
	case *GoogleProvider:
		body := map[string]any{}
		if patch.Title != nil {
			body["summary"] = next.Title
		}
		if patch.Description != nil {
			body["description"] = next.Description
		}
		if patch.Location != nil {
			body["location"] = next.Location
		}
		if patch.Start != nil || patch.End != nil || patch.AllDay != nil {
			end := next.StartAt.Add(time.Hour)
			if next.EndAt != nil {
				end = *next.EndAt
			}
			body["start"] = googleDatePatch(next.StartAt, next.AllDay)
			body["end"] = googleDatePatch(end, next.AllDay)
		}
		return p.doJSON(ctx, "PATCH", gcalEventsURL+"/"+url.PathEscape(id), body, nil)
	case *MicrosoftProvider:
		body := map[string]any{}
		if patch.Title != nil {
			body["subject"] = next.Title
		}
		if patch.Description != nil {
			body["body"] = &msBody{ContentType: "text", Content: next.Description}
		}
		if patch.Location != nil {
			body["location"] = &msLocation{DisplayName: next.Location}
		}
		if patch.Start != nil || patch.End != nil || patch.AllDay != nil {
			end := next.StartAt.Add(time.Hour)
			if next.EndAt != nil {
				end = *next.EndAt
			}
			ev := msEvent{Start: &msDateTime{DateTime: next.StartAt.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"}, End: &msDateTime{DateTime: end.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"}}
			setMicrosoftAllDay(&ev, next.StartAt, end, []bool{next.AllDay})
			body["start"], body["end"], body["isAllDay"] = ev.Start, ev.End, next.AllDay
		}
		return p.doJSON(ctx, "PATCH", graphBase+"/me/events/"+url.PathEscape(id), body, nil)
	}
	return prov.Update(ctx, id, next.Title, next.Description, next.Location, next.StartAt, next.EndAt, next.AllDay)
}

// DecodePatch keeps the field-presence semantics of JSON for tool dispatch.
func DecodePatch(raw []byte) (Patch, error) {
	var p Patch
	err := json.Unmarshal(raw, &p)
	return p, err
}

func googleDatePatch(t time.Time, allDay bool) map[string]any {
	if allDay {
		return map[string]any{"date": t.Format("2006-01-02"), "dateTime": nil, "timeZone": nil}
	}
	return map[string]any{"date": nil, "dateTime": t.Format(time.RFC3339)}
}
