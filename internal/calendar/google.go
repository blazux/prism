package calendar

// GoogleProvider reads/writes the user's primary Google Calendar via the
// Calendar REST API v3, authenticated with the user's own OAuth token. We use
// the REST API (not Google's quirky CalDAV) for reliability, and singleEvents so
// recurring events come back already expanded.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const gcalEventsURL = "https://www.googleapis.com/calendar/v3/calendars/primary/events"

type GoogleProvider struct{ client *http.Client }

func (p *GoogleProvider) Kind() string { return "google" }

type gcalTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
}

func (t *gcalTime) parse() (time.Time, bool) {
	if t == nil {
		return time.Time{}, false
	}
	if t.DateTime != "" {
		if v, err := time.Parse(time.RFC3339, t.DateTime); err == nil {
			return v, true
		}
	}
	if t.Date != "" {
		if v, err := time.ParseInLocation("2006-01-02", t.Date, time.Local); err == nil {
			return v, true
		}
	}
	return time.Time{}, false
}

type gcalEvent struct {
	ID          string    `json:"id,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       *gcalTime `json:"start,omitempty"`
	End         *gcalTime `json:"end,omitempty"`
	Created     string    `json:"created,omitempty"`
}

func (p *GoogleProvider) doJSON(ctx context.Context, method, u string, body, out interface{}) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		return fmt.Errorf("google calendar: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (p *GoogleProvider) List(ctx context.Context, from, to *time.Time) ([]Item, error) {
	start := time.Now().AddDate(-1, 0, 0)
	end := time.Now().AddDate(1, 0, 0)
	if from != nil {
		start = *from
	}
	if to != nil {
		end = *to
	}
	q := url.Values{}
	q.Set("timeMin", start.Format(time.RFC3339))
	q.Set("timeMax", end.Format(time.RFC3339))
	q.Set("singleEvents", "true")
	q.Set("orderBy", "startTime")
	q.Set("maxResults", "2500")
	var res struct {
		Items []gcalEvent `json:"items"`
	}
	if err := p.doJSON(ctx, "GET", gcalEventsURL+"?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(res.Items))
	for _, e := range res.Items {
		it := Item{ID: e.ID, Title: e.Summary, Description: e.Description, Location: e.Location}
		if st, ok := e.Start.parse(); ok {
			it.StartAt = st
		}
		if et, ok := e.End.parse(); ok {
			it.EndAt = &et
		}
		if c, err := time.Parse(time.RFC3339, e.Created); err == nil {
			it.CreatedAt = c
		}
		out = append(out, it)
	}
	return out, nil
}

func (p *GoogleProvider) Add(ctx context.Context, title, description, location string, start time.Time, end *time.Time) (string, error) {
	e := start.Add(time.Hour)
	if end != nil {
		e = *end
	}
	ev := gcalEvent{
		Summary: title, Description: description, Location: location,
		Start: &gcalTime{DateTime: start.Format(time.RFC3339)},
		End:   &gcalTime{DateTime: e.Format(time.RFC3339)},
	}
	var created gcalEvent
	if err := p.doJSON(ctx, "POST", gcalEventsURL, ev, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (p *GoogleProvider) Update(ctx context.Context, id, title, description, location string, start time.Time, end *time.Time) error {
	e := start.Add(time.Hour)
	if end != nil {
		e = *end
	}
	ev := gcalEvent{
		Summary: title, Description: description, Location: location,
		Start: &gcalTime{DateTime: start.Format(time.RFC3339)},
		End:   &gcalTime{DateTime: e.Format(time.RFC3339)},
	}
	return p.doJSON(ctx, "PATCH", gcalEventsURL+"/"+url.PathEscape(id), ev, nil)
}

func (p *GoogleProvider) Delete(ctx context.Context, id string) error {
	return p.doJSON(ctx, "DELETE", gcalEventsURL+"/"+url.PathEscape(id), nil, nil)
}
