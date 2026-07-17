package server

// REST endpoints for the PIM features (notes / tasks / calendar) so widgets and
// apps can read and write them directly. This data is global (shared across all
// workspaces) — see pimScope. Auth is handled by the global middleware.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"prism/internal/calendar"
	"prism/internal/notes"
	"prism/internal/tasks"
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
	prov := notes.ProviderFor(r.Context(), s.memStore, pimScope)
	switch r.Method {
	case "GET":
		items, err := prov.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"notes": items, "source": prov.Kind()})
	case "POST":
		var b struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Body  string `json:"body"`
			Tags  string `json:"tags"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		isNew := strings.TrimSpace(b.ID) == ""
		id, err := prov.Save(r.Context(), b.ID, b.Title, b.Body, b.Tags)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Tell the live agent a note appeared, so it doesn't keep answering from the
		// stale list in its history. Only on CREATE: the editor saves on every blur, so
		// injecting on updates too would spam the conversation. An edit is covered
		// anyway — the app posts the open note's id as context, and the tool reads live.
		if isNew {
			title := strings.TrimSpace(b.Title)
			if title == "" {
				title = "(untitled)"
			}
			s.injectAgentNote(r.URL.Query().Get("session"), fmt.Sprintf(
				"[Notes] The user just created note id=%s titled %q in the Notes app. Any note list you read earlier is out of date — re-read with the note tool before answering about notes.", id, title))
		}
		writeJSON(w, map[string]interface{}{"id": id})
	case "DELETE":
		delID := r.URL.Query().Get("id")
		if err := prov.Delete(r.Context(), delID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.injectAgentNote(r.URL.Query().Get("session"), fmt.Sprintf(
			"[Notes] The user just deleted note id=%s in the Notes app. It no longer exists — re-read with the note tool before answering about notes.", delID))
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// ─── /api/notes/source — choose where notes live (local DB or a Markdown vault) ──

func (s *Server) handleNotesSource(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	switch r.Method {
	case "GET":
		prov, _, _ := s.memStore.GetConfig(r.Context(), notes.KeyProvider)
		path, _, _ := s.memStore.GetConfig(r.Context(), notes.KeyVaultPath)
		if prov == "" {
			prov = "local"
		}
		writeJSON(w, map[string]interface{}{"provider": prov, "path": path})
	case "POST":
		var b struct {
			Provider string `json:"provider"`
			Path     string `json:"path"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Provider == "vault" {
			b.Path = strings.TrimSpace(b.Path)
			if b.Path == "" {
				http.Error(w, "vault path required", 400)
				return
			}
			info, err := os.Stat(b.Path)
			if err != nil || !info.IsDir() {
				http.Error(w, "vault path is not a readable directory (is it mounted into the server container?)", 400)
				return
			}
			s.memStore.SetConfig(r.Context(), notes.KeyVaultPath, b.Path)
			s.memStore.SetConfig(r.Context(), notes.KeyProvider, "vault")
		} else {
			s.memStore.SetConfig(r.Context(), notes.KeyProvider, "local")
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
	prov := tasks.ProviderFor(r.Context(), s.memStore, pimScope)
	switch r.Method {
	case "GET":
		items, err := prov.List(r.Context(), r.URL.Query().Get("include_done") == "true")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"tasks": items, "source": prov.Kind()})
	case "POST":
		var b struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Priority string `json:"priority"`
			Due      string `json:"due"`
			Done     *bool  `json:"done"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.ID != "" && b.Done != nil {
			if err := prov.SetDone(r.Context(), b.ID, *b.Done); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]interface{}{"id": b.ID})
			return
		}
		id, err := prov.Add(r.Context(), b.Title, b.Priority, parsePIMTime(b.Due))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"id": id})
	case "DELETE":
		if err := prov.Delete(r.Context(), r.URL.Query().Get("id")); err != nil {
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
	prov := calendar.ProviderFor(r.Context(), s.memStore, pimScope)
	switch r.Method {
	case "GET":
		items, err := prov.List(r.Context(),
			parsePIMTime(r.URL.Query().Get("from")), parsePIMTime(r.URL.Query().Get("to")))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"events": items, "source": prov.Kind()})
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
		id, err := prov.Add(r.Context(), b.Title, b.Description, b.Location, *start, parsePIMTime(b.End))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"id": id})
	case "DELETE":
		if err := prov.Delete(r.Context(), r.URL.Query().Get("id")); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
