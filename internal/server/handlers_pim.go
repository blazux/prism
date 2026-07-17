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

// pimScope is the fallback scope for personal data when there is no authenticated
// user (no-DB / service identity). With multi-user auth, notes/tasks/calendar are
// scoped per user ("u<id>") — see pimScopeFor. Keep the constant in sync with the
// agent's default PIM scope.
const pimScope = "global"

// pimScopeFor returns the caller's personal PIM scope ("u<id>"), or the global
// fallback for the service identity / legacy no-DB mode.
func (s *Server) pimScopeFor(r *http.Request) string {
	if u := currentUser(r); u != nil && u.ID > 0 {
		return fmt.Sprintf("u%d", u.ID)
	}
	return pimScope
}

// pimGroupScopeFor returns the caller's group note scope ("g<id>", their primary
// group) plus the group id. ok is false when the user belongs to no group.
func (s *Server) pimGroupScopeFor(r *http.Request) (scope string, gid int64, ok bool) {
	u := currentUser(r)
	if u == nil || u.ID == 0 {
		return "", 0, false
	}
	if ms := s.store(); ms != nil {
		if groups, err := ms.UserGroups(r.Context(), u.ID); err == nil && len(groups) > 0 {
			return fmt.Sprintf("g%d", groups[0].GroupID), groups[0].GroupID, true
		}
	}
	return "", 0, false
}

// noteJSON is the wire shape for a note in the app: the base fields plus which
// scope it lives in ("personal" | "group") and whether the caller may edit it.
func noteJSON(id, title, body, tags string, createdAt, updatedAt time.Time, scope string, canEdit bool, ownerID, originID int64) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "title": title, "body": body, "tags": tags,
		"createdAt": createdAt, "updatedAt": updatedAt,
		"scope": scope, "canEdit": canEdit, "ownerId": ownerID, "originId": originID,
	}
}

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
	prov := notes.ProviderFor(r.Context(), s.userStore(r), s.pimScopeFor(r))
	switch r.Method {
	case "GET":
		items, err := prov.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out := make([]map[string]interface{}, 0, len(items))
		for _, n := range items {
			out = append(out, noteJSON(n.ID, n.Title, n.Body, n.Tags, n.CreatedAt, n.UpdatedAt, "personal", true, 0, 0))
		}
		// Append the group's shared notes (read-only unless the caller shared them,
		// or is a group admin). These live under the group scope "g<id>".
		if gscope, gid, ok := s.pimGroupScopeFor(r); ok {
			u := currentUser(r)
			admin := s.isGroupAdminOf(r.Context(), u, gid)
			if gnotes, err := s.store().ListNotes(r.Context(), gscope); err == nil {
				for _, n := range gnotes {
					canEdit := admin || (u != nil && n.OwnerID == u.ID)
					out = append(out, noteJSON(strconv.FormatInt(n.ID, 10), n.Title, n.Body, n.Tags, n.CreatedAt, n.UpdatedAt, "group", canEdit, n.OwnerID, n.OriginID))
				}
			}
		}
		writeJSON(w, map[string]interface{}{"notes": out, "source": prov.Kind()})
	case "POST":
		var b struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Body  string `json:"body"`
			Tags  string `json:"tags"`
			Scope string `json:"scope"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		// Editing a shared group note: allowed for its owner or a group admin only.
		if b.Scope == "group" {
			gscope, gid, ok := s.pimGroupScopeFor(r)
			if !ok {
				http.Error(w, "not in a group", 400)
				return
			}
			id, _ := strconv.ParseInt(b.ID, 10, 64)
			n, err := s.store().GetNote(r.Context(), gscope, id)
			if err != nil {
				http.Error(w, "note not found", 404)
				return
			}
			u := currentUser(r)
			if !s.isGroupAdminOf(r.Context(), u, gid) && !(u != nil && n.OwnerID == u.ID) {
				http.Error(w, "read-only: only the author or a group admin can edit this shared note", 403)
				return
			}
			if err := s.store().UpdateNote(r.Context(), gscope, id, b.Title, b.Body, b.Tags); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]interface{}{"id": b.ID})
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
		if r.URL.Query().Get("scope") == "group" {
			gscope, gid, ok := s.pimGroupScopeFor(r)
			if !ok {
				http.Error(w, "not in a group", 400)
				return
			}
			id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
			n, err := s.store().GetNote(r.Context(), gscope, id)
			if err != nil {
				http.Error(w, "note not found", 404)
				return
			}
			u := currentUser(r)
			if !s.isGroupAdminOf(r.Context(), u, gid) && !(u != nil && n.OwnerID == u.ID) {
				http.Error(w, "read-only: only the author or a group admin can remove this shared note", 403)
				return
			}
			if err := s.store().DeleteNote(r.Context(), gscope, id); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]interface{}{"ok": true})
			return
		}
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

// ─── /api/notes/share — publish a personal note to the caller's group ───────────

// POST /api/notes/share?id=<personalNoteId> shares (or re-shares) a personal note
// to the group, read-only for everyone but the author and group admins.
// DELETE /api/notes/share?id=<groupNoteId> unshares it (author or admin only).
func (s *Server) handleNoteShare(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	gscope, gid, ok := s.pimGroupScopeFor(r)
	if !ok {
		http.Error(w, "not in a group", 400)
		return
	}
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id == 0 {
		http.Error(w, "id required", 400)
		return
	}
	switch r.Method {
	case "POST":
		// The source must be one of the caller's own personal notes.
		src, err := s.store().GetNote(r.Context(), s.pimScopeFor(r), id)
		if err != nil {
			http.Error(w, "note not found", 404)
			return
		}
		newID, err := s.store().ShareNote(r.Context(), gscope, u.ID, src.ID, src.Title, src.Body, src.Tags)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"id": strconv.FormatInt(newID, 10), "shared": true})
	case "DELETE":
		n, err := s.store().GetNote(r.Context(), gscope, id)
		if err != nil {
			http.Error(w, "note not found", 404)
			return
		}
		if !s.isGroupAdminOf(r.Context(), u, gid) && n.OwnerID != u.ID {
			http.Error(w, "only the author or a group admin can unshare", 403)
			return
		}
		if err := s.store().DeleteNote(r.Context(), gscope, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
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
		prov, _, _ := s.userStore(r).GetConfig(r.Context(), notes.KeyProvider)
		path, _, _ := s.userStore(r).GetConfig(r.Context(), notes.KeyVaultPath)
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
			s.userStore(r).SetConfig(r.Context(), notes.KeyVaultPath, b.Path)
			s.userStore(r).SetConfig(r.Context(), notes.KeyProvider, "vault")
		} else {
			s.userStore(r).SetConfig(r.Context(), notes.KeyProvider, "local")
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
	prov := tasks.ProviderFor(r.Context(), s.userStore(r), s.pimScopeFor(r))
	switch r.Method {
	case "GET":
		items, err := prov.List(r.Context(), r.URL.Query().Get("include_done") == "true")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Vortex: outbound calls the agent still has to place are tasks too — show them
		// in the same list (read-only; the agent places and cancels them).
		//
		// Admins only. Tasks are scoped per user (session_id = "u<id>"), but Vox's call
		// queue has no owner column: every row would otherwise land in *everyone's* list,
		// leaking who is being called and why. That matches the tools, which are
		// admin-only by default (see defaultAdminOnly).
		if u := currentUser(r); u != nil && s.isAdminUser(r.Context(), u) {
			items = append(s.voxPendingCallTasks(r.Context()), items...)
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
		if isCallTaskID(b.ID) {
			writeErr(w, http.StatusBadRequest, callTaskReadOnlyMsg)
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
		if id := r.URL.Query().Get("id"); isCallTaskID(id) {
			writeErr(w, http.StatusBadRequest, callTaskReadOnlyMsg)
			return
		}
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
	prov := calendar.ProviderFor(r.Context(), s.userStore(r), s.pimScopeFor(r))
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
