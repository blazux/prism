package tasks

// TodoistProvider talks to the Todoist API v1 using a personal API token
// (Settings → Integrations → Developer in Todoist) — no OAuth needed. Todoist's
// REST API only returns active tasks, so completed ones are not listed.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"prism/internal/timeprefs"
	"time"

	"prism/internal/memory"
)

const (
	TodoistTokenSecret = "todoist_token"
	todoistBase        = "https://api.todoist.com/api/v1"
)

func todoistToken(ctx context.Context, store *memory.Store) (string, error) {
	if store == nil {
		return "", nil
	}
	t, _, err := store.GetSecret(ctx, TodoistTokenSecret)
	return t, err
}

// baseURL is empty in production and points at a test server in tests.
type TodoistProvider struct {
	token   string
	baseURL string
}

// NewTodoistProvider builds a provider for a raw token (used for validation).
func NewTodoistProvider(token string) *TodoistProvider { return &TodoistProvider{token: token} }

func (p *TodoistProvider) base() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return todoistBase
}

func (p *TodoistProvider) Kind() string { return "todoist" }

func (p *TodoistProvider) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base()+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return (&http.Client{Timeout: 20 * time.Second}).Do(req)
}

type todoistTask struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	Priority    int    `json:"priority"`
	IsCompleted bool   `json:"is_completed"`
	CreatedAt   string `json:"created_at"`
	AddedAt     string `json:"added_at"`
	Checked     bool   `json:"checked"`
	Due         *struct {
		Date     string `json:"date"`
		Datetime string `json:"datetime"`
	} `json:"due"`
}

// List returns the ACTIVE tasks. Todoist's REST task endpoint has no completed
// tasks to give (completed history has a separate endpoint), so includeDone cannot be
// honoured here: an absent task is not proof that it does not exist, which is
// why taskLookup no longer guards non-local providers.
func (p *TodoistProvider) List(ctx context.Context, includeDone bool) ([]Item, error) {
	_ = includeDone
	var ts []todoistTask
	cursor := ""
	seen := map[string]bool{}
	for {
		path := "/tasks"
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		resp, err := p.do(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			err = todoistErr(resp)
			resp.Body.Close()
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		// The array shape remains accepted for existing compatible proxies.
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
			if err = json.Unmarshal(raw, &ts); err != nil {
				return nil, err
			}
			break
		}
		var page struct {
			Results []todoistTask `json:"results"`
			Next    string        `json:"next_cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		ts = append(ts, page.Results...)
		if page.Next == "" {
			break
		}
		if seen[page.Next] {
			return nil, fmt.Errorf("Todoist repeated its pagination cursor")
		}
		seen[page.Next] = true
		cursor = page.Next
	}
	out := make([]Item, 0, len(ts))
	for _, t := range ts {
		it := Item{ID: t.ID, Title: t.Content, Done: t.IsCompleted || t.Checked, Priority: fromTodoistPriority(t.Priority)}
		if t.CreatedAt == "" {
			t.CreatedAt = t.AddedAt
		}
		if c, err := time.Parse(time.RFC3339, t.CreatedAt); err == nil {
			it.CreatedAt = c
		}
		if t.Due != nil {
			// Branch on what actually parsed, not on what is merely present:
			// Todoist sends a floating datetime for a task with no timezone,
			// which is not RFC3339, and the plain date sitting next to it in
			// the same response was never tried. The task then came back with
			// no due date at all and stopped looking urgent.
			if d, err := time.Parse(time.RFC3339, t.Due.Datetime); t.Due.Datetime != "" && err == nil {
				it.DueAt = &d
			} else if d, err := time.ParseInLocation("2006-01-02T15:04:05", t.Due.Datetime, timeprefs.Location(ctx)); t.Due.Datetime != "" && err == nil {
				it.DueAt = &d
			} else if d, err := time.Parse(time.RFC3339, t.Due.Date); err == nil {
				it.DueAt = &d
			} else if d, err := time.ParseInLocation("2006-01-02T15:04:05", t.Due.Date, timeprefs.Location(ctx)); err == nil {
				it.DueAt = &d
			} else if d, err := time.ParseInLocation("2006-01-02", t.Due.Date, timeprefs.Location(ctx)); t.Due.Date != "" && err == nil {
				it.DueAt = &d
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func (p *TodoistProvider) Add(ctx context.Context, title, priority string, due *time.Time) (string, error) {
	payload := map[string]interface{}{"content": title, "priority": toTodoistPriority(priority)}
	if due != nil {
		payload["due_datetime"] = due.UTC().Format(time.RFC3339)
	}
	body, _ := json.Marshal(payload)
	resp, err := p.do(ctx, "POST", "/tasks", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", todoistErr(resp)
	}
	var t todoistTask
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return "", err
	}
	return t.ID, nil
}

func (p *TodoistProvider) SetDone(ctx context.Context, id string, done bool) error {
	action := "close"
	if !done {
		action = "reopen"
	}
	resp, err := p.do(ctx, "POST", "/tasks/"+id+"/"+action, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return todoistErr(resp)
	}
	return nil
}

func (p *TodoistProvider) Delete(ctx context.Context, id string) error {
	resp, err := p.do(ctx, "DELETE", "/tasks/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return todoistErr(resp)
	}
	return nil
}

func todoistErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	return fmt.Errorf("todoist: %s: %s", resp.Status, bytes.TrimSpace(b))
}

// Todoist priority: 4 = highest (p1) … 1 = lowest (p4, also Todoist's own
// default for a task with no priority set). We expose three tiers
// (high/normal/low); "low" and "normal" used to both write as 1, silently
// collapsing an explicit "low" into "normal" with no way to tell them apart
// again on the next read. Distinct values now — high=4, normal=2, low=1 —
// so a priority set through Prism round-trips correctly. Trade-off: a task
// created directly in Todoist with no priority set (also value 1) now reads
// back as "low" rather than "normal"; that's a labeling choice for
// never-explicitly-prioritized tasks, not data loss, and unavoidable given
// Todoist's own default already occupies the scale's floor value.
func toTodoistPriority(p string) int {
	switch p {
	case "high":
		return 4
	case "low":
		return 1
	default: // "normal", or anything unrecognized
		return 2
	}
}

func fromTodoistPriority(p int) string {
	switch {
	case p >= 3:
		return "high"
	case p == 2:
		return "normal"
	default: // p <= 1
		return "low"
	}
}
