package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"prism/internal/calendar"
	"prism/internal/notes"
	"prism/internal/tasks"
)

// pimScope is the shared scope for personal data (notes, tasks, calendar). These
// apps are global ("soft partition"): every workspace's agent sees the same set,
// while dashboard widgets stay per-workspace.
const pimScope = "global"

// parseTime accepts a few human-friendly layouts (local time) and RFC3339.
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	layouts := []string{
		time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse time %q (use e.g. 2006-01-02 15:04)", s)
}

func parseTimePtr(s string) *time.Time {
	if t, err := parseTime(s); err == nil {
		return &t
	}
	return nil
}

// ─── note ─────────────────────────────────────────────────────────────────────

func (e *ToolExecutor) noteTool(ctx context.Context, action, idStr, title, body, tags string) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("notes unavailable: no database")
	}
	prov := notes.ProviderFor(ctx, e.memStore, pimScope)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add", "create":
		id, err := prov.Save(ctx, "", title, body, tags)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Note %q added.", id), nil
	case "list", "":
		items, err := prov.List(ctx)
		if err != nil {
			return "", err
		}
		if len(items) == 0 {
			return "No notes.", nil
		}
		return jsonResult(items), nil
	case "update", "edit":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("update requires an id")
		}
		if _, err := prov.Save(ctx, idStr, title, body, tags); err != nil {
			return "", err
		}
		return fmt.Sprintf("Note %q updated.", idStr), nil
	case "delete", "remove":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("delete requires an id")
		}
		if err := prov.Delete(ctx, idStr); err != nil {
			return "", err
		}
		return fmt.Sprintf("Note %q deleted.", idStr), nil
	default:
		return "", fmt.Errorf("note: unknown action %q (add, list, update, delete)", action)
	}
}

// ─── task ─────────────────────────────────────────────────────────────────────

func (e *ToolExecutor) taskTool(ctx context.Context, action, idStr, title, priority, due string, includeDone bool) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("tasks unavailable: no database")
	}
	prov := tasks.ProviderFor(ctx, e.memStore, pimScope)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add", "create":
		id, err := prov.Add(ctx, title, priority, parseTimePtr(due))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Task %q added.", id), nil
	case "list", "":
		items, err := prov.List(ctx, includeDone)
		if err != nil {
			return "", err
		}
		if len(items) == 0 {
			return "No tasks.", nil
		}
		return jsonResult(items), nil
	case "done", "complete":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("done requires an id")
		}
		if err := prov.SetDone(ctx, idStr, true); err != nil {
			return "", err
		}
		return fmt.Sprintf("Task %q completed.", idStr), nil
	case "reopen", "undone":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("reopen requires an id")
		}
		if err := prov.SetDone(ctx, idStr, false); err != nil {
			return "", err
		}
		return fmt.Sprintf("Task %q reopened.", idStr), nil
	case "delete", "remove":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("delete requires an id")
		}
		if err := prov.Delete(ctx, idStr); err != nil {
			return "", err
		}
		return fmt.Sprintf("Task %q deleted.", idStr), nil
	default:
		return "", fmt.Errorf("task: unknown action %q (add, list, done, reopen, delete)", action)
	}
}

// ─── calendar ─────────────────────────────────────────────────────────────────

func (e *ToolExecutor) calendarTool(ctx context.Context, action, idStr, title, description, location, start, end, from, to string) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("calendar unavailable: no database")
	}
	prov := calendar.ProviderFor(ctx, e.memStore, pimScope)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add", "create":
		st, err := parseTime(start)
		if err != nil {
			return "", fmt.Errorf("add requires a valid start time: %w", err)
		}
		id, err := prov.Add(ctx, title, description, location, st, parseTimePtr(end))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Event %q added.", id), nil
	case "list", "":
		items, err := prov.List(ctx, parseTimePtr(from), parseTimePtr(to))
		if err != nil {
			return "", err
		}
		if len(items) == 0 {
			return "No events.", nil
		}
		return jsonResult(items), nil
	case "delete", "remove":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("delete requires an id")
		}
		if err := prov.Delete(ctx, idStr); err != nil {
			return "", err
		}
		return fmt.Sprintf("Event %q deleted.", idStr), nil
	default:
		return "", fmt.Errorf("calendar: unknown action %q (add, list, delete)", action)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseID(s string) int64 {
	var id int64
	fmt.Sscan(strings.TrimSpace(s), &id)
	return id
}

// idArg extracts an "id" tool argument that the model may send as a JSON number
// or a string.
func idArg(args map[string]interface{}) string {
	switch v := args["id"].(type) {
	case float64:
		return fmt.Sprintf("%d", int64(v))
	case string:
		return v
	}
	return ""
}

func jsonResult(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
