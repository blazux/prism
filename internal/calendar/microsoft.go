package calendar

// MicrosoftProvider reads/writes the user's Outlook / Microsoft 365 calendar via
// the Microsoft Graph API, authenticated with the user's own OAuth token. We use
// calendarView so recurring events come back already expanded, and request times
// in UTC so parsing is unambiguous.

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

const graphBase = "https://graph.microsoft.com/v1.0"

type MicrosoftProvider struct{ client *http.Client }

func (p *MicrosoftProvider) Kind() string { return "microsoft" }

type msDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone,omitempty"`
}

// parse reads Graph's "2006-01-02T15:04:05.0000000" (requested in UTC).
func (t *msDateTime) parse() (time.Time, bool) {
	if t == nil || len(t.DateTime) < 19 {
		return time.Time{}, false
	}
	v, err := time.ParseInLocation("2006-01-02T15:04:05", t.DateTime[:19], time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return v, true
}

type msEvent struct {
	ID       string      `json:"id,omitempty"`
	Subject  string      `json:"subject,omitempty"`
	Body     *msBody     `json:"body,omitempty"`
	Location *msLocation `json:"location,omitempty"`
	Start    *msDateTime `json:"start,omitempty"`
	End      *msDateTime `json:"end,omitempty"`
	Created  string      `json:"createdDateTime,omitempty"`
}

type msBody struct {
	ContentType string `json:"contentType,omitempty"`
	Content     string `json:"content,omitempty"`
}

type msLocation struct {
	DisplayName string `json:"displayName,omitempty"`
}

func (p *MicrosoftProvider) doJSON(ctx context.Context, method, u string, body, out interface{}) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	req.Header.Set("Prefer", `outlook.timezone="UTC"`)
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
		return fmt.Errorf("microsoft graph: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (p *MicrosoftProvider) List(ctx context.Context, from, to *time.Time) ([]Item, error) {
	start := time.Now().AddDate(-1, 0, 0)
	end := time.Now().AddDate(1, 0, 0)
	if from != nil {
		start = *from
	}
	if to != nil {
		end = *to
	}
	q := url.Values{}
	q.Set("startDateTime", start.UTC().Format(time.RFC3339))
	q.Set("endDateTime", end.UTC().Format(time.RFC3339))
	q.Set("$orderby", "start/dateTime")
	q.Set("$top", "1000")
	var res struct {
		Value []msEvent `json:"value"`
	}
	if err := p.doJSON(ctx, "GET", graphBase+"/me/calendarView?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(res.Value))
	for _, e := range res.Value {
		it := Item{ID: e.ID, Title: e.Subject}
		if e.Body != nil {
			it.Description = e.Body.Content
		}
		if e.Location != nil {
			it.Location = e.Location.DisplayName
		}
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

func (p *MicrosoftProvider) Add(ctx context.Context, title, description, location string, start time.Time, end *time.Time) (string, error) {
	e := start.Add(time.Hour)
	if end != nil {
		e = *end
	}
	ev := msEvent{
		Subject: title,
		Start:   &msDateTime{DateTime: start.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
		End:     &msDateTime{DateTime: e.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
	}
	if description != "" {
		ev.Body = &msBody{ContentType: "text", Content: description}
	}
	if location != "" {
		ev.Location = &msLocation{DisplayName: location}
	}
	var created msEvent
	if err := p.doJSON(ctx, "POST", graphBase+"/me/events", ev, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (p *MicrosoftProvider) Update(ctx context.Context, id, title, description, location string, start time.Time, end *time.Time) error {
	e := start.Add(time.Hour)
	if end != nil {
		e = *end
	}
	ev := msEvent{
		Subject: title,
		Start:   &msDateTime{DateTime: start.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
		End:     &msDateTime{DateTime: e.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
	}
	if description != "" {
		ev.Body = &msBody{ContentType: "text", Content: description}
	}
	if location != "" {
		ev.Location = &msLocation{DisplayName: location}
	}
	return p.doJSON(ctx, "PATCH", graphBase+"/me/events/"+url.PathEscape(id), ev, nil)
}

func (p *MicrosoftProvider) Delete(ctx context.Context, id string) error {
	return p.doJSON(ctx, "DELETE", graphBase+"/me/events/"+url.PathEscape(id), nil, nil)
}
