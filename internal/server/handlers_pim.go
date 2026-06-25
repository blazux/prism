package server

// REST endpoints for the PIM features (notes / tasks / calendar) so widgets and
// apps can read and write them directly. This data is global (shared across all
// workspaces) — see pimScope. Auth is handled by the global middleware.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// pimScope is the shared scope for personal data — notes, tasks and calendar are
// global ("soft partition"), shared across all workspaces. Keep in sync with the
// agent's pimScope constant.
const pimScope = "global"

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) pimStore(w http.ResponseWriter) bool {
	if s.memStore == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// parsePIMTime accepts ISO-8601 (what widgets send) plus a couple of friendly
// local layouts. Returns nil for an empty string.
func parsePIMTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, l := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return &t
		}
	}
	return nil
}

func idParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	return id
}

// ─── /api/notes ───────────────────────────────────────────────────────────────

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	sess := pimScope
	switch r.Method {
	case "GET":
		notes, err := s.memStore.ListNotes(r.Context(), sess)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"notes": notes})
	case "POST":
		var b struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Body  string `json:"body"`
			Tags  string `json:"tags"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.ID > 0 {
			if err := s.memStore.UpdateNote(r.Context(), sess, b.ID, b.Title, b.Body, b.Tags); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]interface{}{"id": b.ID})
			return
		}
		id, err := s.memStore.AddNote(r.Context(), sess, b.Title, b.Body, b.Tags)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"id": id})
	case "DELETE":
		if err := s.memStore.DeleteNote(r.Context(), sess, idParam(r)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// ─── /api/tasks ───────────────────────────────────────────────────────────────

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	sess := pimScope
	switch r.Method {
	case "GET":
		tasks, err := s.memStore.ListTasks(r.Context(), sess, r.URL.Query().Get("include_done") == "true")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"tasks": tasks})
	case "POST":
		var b struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Priority string `json:"priority"`
			Due      string `json:"due"`
			Done     *bool  `json:"done"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.ID > 0 && b.Done != nil {
			if err := s.memStore.SetTaskDone(r.Context(), sess, b.ID, *b.Done); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]interface{}{"id": b.ID})
			return
		}
		id, err := s.memStore.AddTask(r.Context(), sess, b.Title, b.Priority, parsePIMTime(b.Due))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"id": id})
	case "DELETE":
		if err := s.memStore.DeleteTask(r.Context(), sess, idParam(r)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// ─── /api/events ──────────────────────────────────────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	sess := pimScope
	switch r.Method {
	case "GET":
		events, err := s.memStore.ListEvents(r.Context(), sess,
			parsePIMTime(r.URL.Query().Get("from")), parsePIMTime(r.URL.Query().Get("to")))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"events": events})
	case "POST":
		var b struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Location    string `json:"location"`
			Start       string `json:"start"`
			End         string `json:"end"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		start := parsePIMTime(b.Start)
		if start == nil {
			http.Error(w, "valid start time required", 400)
			return
		}
		id, err := s.memStore.AddEvent(r.Context(), sess, b.Title, b.Description, b.Location, *start, parsePIMTime(b.End))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"id": id})
	case "DELETE":
		if err := s.memStore.DeleteEvent(r.Context(), sess, idParam(r)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
