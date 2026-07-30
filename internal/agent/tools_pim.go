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

// PIMScope is the fallback scope for personal data (notes, tasks, calendar) when
// the session has no user prefix (legacy single-owner / no-DB). With multi-user
// auth each user's PIM is scoped to "u<id>" — see pimSessionScope. Exported so
// internal/server's pimScopeFor (handlers_pim.go) shares this exact constant
// instead of keeping its own copy in sync by hand.
const PIMScope = "global"

// pimSessionScope returns the caller's personal PIM scope ("u<id>"), matching the
// server's pimScopeFor so the agent and the Notes app see the same data. Shared
// agents (no user prefix) fall back to the group scope, then the global default.
func (e *ToolExecutor) pimSessionScope() string {
	if s := e.personalScope(); s != "" {
		return s
	}
	return PIMScope
}

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

// pimHint formats an "Existing <kind>: id ("title"), …" line for embedding in
// not-found errors, so a bad id teaches the model the valid ones instead of
// leaving it to guess again (or, worse, silently no-op: the DB UPDATE/DELETE
// statements match zero rows without complaint).
func pimHint(kind string, ids, titles []string) string {
	if len(ids) == 0 {
		return "There are no " + kind + "."
	}
	parts := make([]string, len(ids))
	for i := range ids {
		parts[i] = fmt.Sprintf("%s (%q)", ids[i], titles[i])
	}
	return "Existing " + kind + ": " + strings.Join(parts, ", ") + " — use one of these ids verbatim."
}

// noteLookup finds a note by id. Found → (item, ""). Not found → (nil, hint)
// with the hint listing what actually exists. If the provider can't list
// (nil, "") — the caller proceeds unguarded rather than blocking the action.
func noteLookup(ctx context.Context, prov notes.Provider, id string) (*notes.Item, string) {
	items, err := prov.List(ctx)
	if err != nil {
		return nil, ""
	}
	want := strings.TrimSpace(id)
	ids := make([]string, len(items))
	titles := make([]string, len(items))
	for i := range items {
		if items[i].ID == want {
			return &items[i], ""
		}
		ids[i], titles[i] = items[i].ID, items[i].Title
	}
	return nil, pimHint("notes", ids, titles)
}

// ─── note ─────────────────────────────────────────────────────────────────────

func (e *ToolExecutor) noteTool(ctx context.Context, action, idStr, title, body, tags string) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("notes unavailable: no database")
	}
	prov := notes.ProviderFor(ctx, e.userStore(), e.pimSessionScope())
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
		// Group members also see the notes shared to their group (read-only): the
		// Notes app shows them, so the agent should know them too. They live under
		// the group scope ("g<id>") in the local store, whatever the personal
		// notes provider is.
		type sharedNote struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			Tags   string `json:"tags"`
			Shared bool   `json:"shared"`
			Note   string `json:"note,omitempty"`
		}
		var shared []sharedNote
		if gscope := e.ragScope; gscope != "" && gscope != e.pimSessionScope() && strings.HasPrefix(gscope, "g") {
			if gnotes, err := e.memStore.ListNotes(ctx, gscope); err == nil {
				for _, n := range gnotes {
					shared = append(shared, sharedNote{
						ID: fmt.Sprintf("%d", n.ID), Title: n.Title, Body: n.Body, Tags: n.Tags,
						Shared: true, Note: "shared with the group — read-only for you",
					})
				}
			}
		}
		if len(items) == 0 && len(shared) == 0 {
			return "No notes.", nil
		}
		out := jsonResult(items)
		if len(shared) > 0 {
			out += "\n\nGroup-shared notes (read-only):\n" + jsonResult(shared)
		}
		return out, nil
	case "update", "edit":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("update requires an id")
		}
		if item, hint := noteLookup(ctx, prov, idStr); item == nil && hint != "" {
			return "", fmt.Errorf("note %q not found. %s", idStr, hint)
		}
		if _, err := prov.Save(ctx, idStr, title, body, tags); err != nil {
			return "", err
		}
		return fmt.Sprintf("Note %q updated.", idStr), nil
	case "delete", "remove":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("delete requires an id")
		}
		item, hint := noteLookup(ctx, prov, idStr)
		if item == nil && hint != "" {
			return "", fmt.Errorf("note %q not found. %s", idStr, hint)
		}
		if err := prov.Delete(ctx, idStr); err != nil {
			return "", err
		}
		msg := fmt.Sprintf("Note %q deleted.", idStr)
		if item != nil {
			// Echo what was destroyed: if this delete was a mistake, the content
			// stays in the conversation and the agent can recreate it itself.
			msg += fmt.Sprintf(" Deleted content (recreate it with 'add' if this was a mistake) — title: %q, tags: %q, body:\n%s", item.Title, item.Tags, item.Body)
		}
		return msg, nil
	default:
		return "", fmt.Errorf("note: unknown action %q (add, list, update, delete)", action)
	}
}

// taskLookup is noteLookup for tasks (includes completed tasks, so re-marking
// a done task or deleting one still resolves).
func taskLookup(ctx context.Context, prov tasks.Provider, id string) (*tasks.Item, string) {
	items, err := prov.List(ctx, true)
	if err != nil {
		return nil, ""
	}
	want := strings.TrimSpace(id)
	ids := make([]string, len(items))
	titles := make([]string, len(items))
	for i := range items {
		if items[i].ID == want {
			return &items[i], ""
		}
		ids[i], titles[i] = items[i].ID, items[i].Title
	}
	return nil, pimHint("tasks", ids, titles)
}

// ─── task ─────────────────────────────────────────────────────────────────────

func (e *ToolExecutor) taskTool(ctx context.Context, action, idStr, title, priority, due string, includeDone bool) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("tasks unavailable: no database")
	}
	prov := tasks.ProviderFor(ctx, e.userStore(), e.pimSessionScope())
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
		if item, hint := taskLookup(ctx, prov, idStr); item == nil && hint != "" {
			return "", fmt.Errorf("task %q not found. %s", idStr, hint)
		}
		if err := prov.SetDone(ctx, idStr, true); err != nil {
			return "", err
		}
		return fmt.Sprintf("Task %q completed.", idStr), nil
	case "reopen", "undone":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("reopen requires an id")
		}
		if item, hint := taskLookup(ctx, prov, idStr); item == nil && hint != "" {
			return "", fmt.Errorf("task %q not found. %s", idStr, hint)
		}
		if err := prov.SetDone(ctx, idStr, false); err != nil {
			return "", err
		}
		return fmt.Sprintf("Task %q reopened.", idStr), nil
	case "delete", "remove":
		if strings.TrimSpace(idStr) == "" {
			return "", fmt.Errorf("delete requires an id")
		}
		item, hint := taskLookup(ctx, prov, idStr)
		if item == nil && hint != "" {
			return "", fmt.Errorf("task %q not found. %s", idStr, hint)
		}
		if err := prov.Delete(ctx, idStr); err != nil {
			return "", err
		}
		msg := fmt.Sprintf("Task %q deleted.", idStr)
		if item != nil {
			msg += fmt.Sprintf(" Deleted task was: %q (priority %s).", item.Title, item.Priority)
		}
		return msg, nil
	default:
		return "", fmt.Errorf("task: unknown action %q (add, list, done, reopen, delete)", action)
	}
}

// ─── calendar ─────────────────────────────────────────────────────────────────

func (e *ToolExecutor) calendarTool(ctx context.Context, action, idStr, title, description, location, start, end, from, to string) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("calendar unavailable: no database")
	}
	prov := calendar.ProviderFor(ctx, e.userStore(), e.pimSessionScope())
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
		// Guard local events only: an unbounded List against an external
		// provider (Google/CalDAV) can be truncated, which would turn a real
		// event into a false "not found" and block a legitimate delete.
		var deleted *calendar.Item
		if prov.Kind() == "local" {
			if items, err := prov.List(ctx, nil, nil); err == nil {
				want := strings.TrimSpace(idStr)
				ids := make([]string, len(items))
				titles := make([]string, len(items))
				for i := range items {
					if items[i].ID == want {
						deleted = &items[i]
						break
					}
					ids[i], titles[i] = items[i].ID, items[i].Title
				}
				if deleted == nil {
					return "", fmt.Errorf("event %q not found. %s", idStr, pimHint("events", ids, titles))
				}
			}
		}
		if err := prov.Delete(ctx, idStr); err != nil {
			return "", err
		}
		msg := fmt.Sprintf("Event %q deleted.", idStr)
		if deleted != nil {
			msg += fmt.Sprintf(" Deleted event was: %q at %s.", deleted.Title, deleted.StartAt.Format("2006-01-02 15:04"))
		}
		return msg, nil
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
