package tasks

// TodoistProvider talks to the Todoist REST API v2 using a personal API token
// (Settings → Integrations → Developer in Todoist) — no OAuth needed. Todoist's
// REST API only returns active tasks, so completed ones are not listed.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"prism/internal/memory"
)

const (
	TodoistTokenSecret = "todoist_token"
	todoistBase        = "https://api.todoist.com/rest/v2"
)

func todoistToken(ctx context.Context, store *memory.Store) string {
	if store == nil {
		return ""
	}
	t, _, _ := store.GetSecret(ctx, TodoistTokenSecret)
	return t
}

type TodoistProvider struct{ token string }

// NewTodoistProvider builds a provider for a raw token (used for validation).
func NewTodoistProvider(token string) *TodoistProvider { return &TodoistProvider{token: token} }

func (p *TodoistProvider) Kind() string { return "todoist" }

func (p *TodoistProvider) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, todoistBase+path, r)
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
	Due         *struct {
		Date     string `json:"date"`
		Datetime string `json:"datetime"`
	} `json:"due"`
}

func (p *TodoistProvider) List(ctx context.Context, includeDone bool) ([]Item, error) {
	resp, err := p.do(ctx, "GET", "/tasks", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, todoistErr(resp)
	}
	var ts []todoistTask
	if err := json.NewDecoder(resp.Body).Decode(&ts); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(ts))
	for _, t := range ts {
		it := Item{ID: t.ID, Title: t.Content, Done: t.IsCompleted, Priority: fromTodoistPriority(t.Priority)}
		if c, err := time.Parse(time.RFC3339, t.CreatedAt); err == nil {
			it.CreatedAt = c
		}
		if t.Due != nil {
			if t.Due.Datetime != "" {
				if d, err := time.Parse(time.RFC3339, t.Due.Datetime); err == nil {
					it.DueAt = &d
				}
			} else if t.Due.Date != "" {
				if d, err := time.ParseInLocation("2006-01-02", t.Due.Date, time.Local); err == nil {
					it.DueAt = &d
				}
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
