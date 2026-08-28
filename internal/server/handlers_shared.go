package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"prism/internal/memory"
)

// Sharing widgets and dashboards within a group. A member publishes a widget
// (or a whole board) to one of their groups; other members of that group add
// it to their own dashboard. Scoped to groups on purpose: other groups are
// other teams with different subjects. Multi-user only — there are no groups
// to share within in single-user mode.

type sharedWidget struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Cols    int    `json:"cols"`
	Height  int    `json:"height"`
}

type sharedPayload struct {
	Widgets []sharedWidget `json:"widgets"`
}

// shareSlug reduces a title to a filesystem/id-safe slug (server-side twin of
// the agent's slugify).
func shareSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "widget"
	}
	return out
}

// sessionWidgets returns the widgets of a session: all of them, or just the one
// with id==only when only is non-empty.
func (s *Server) sessionWidgets(session, only string) []sharedWidget {
	dir := filepath.Join(s.cfg.PluginDir, session)
	var out []sharedWidget
	for _, p := range s.loadPlugins(dir) {
		if only != "" && p.id != only {
			continue
		}
		out = append(out, sharedWidget{Title: p.title, Content: p.content, Cols: p.cols, Height: p.height})
	}
	return out
}

// pushPluginToSession renders a freshly added widget on every live dashboard of
// a session, using the same plugin_load event the agent's add path emits.
func (s *Server) pushPluginToSession(session, id, title, content string, cols, height int) {
	msg, _ := json.Marshal(map[string]interface{}{
		"type": "plugin_load", "id": id, "title": title, "content": content, "cols": cols, "height": height,
	})
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if c.sessionID == session {
			select {
			case c.send <- msg:
			default:
			}
		}
	}
}

// callerGroupIDs returns the set of groups the caller belongs to.
func (s *Server) callerGroupIDs(r *http.Request) map[int64]bool {
	set := map[int64]bool{}
	u := currentUser(r)
	if u == nil || u.ID == 0 {
		return set
	}
	if ms := s.store(); ms != nil {
		if gs, err := ms.UserGroups(r.Context(), u.ID); err == nil {
			for _, g := range gs {
				set[g.GroupID] = true
			}
		}
	}
	return set
}

// handleShared: GET lists items shared to the caller's groups; POST publishes.
func (s *Server) handleShared(w http.ResponseWriter, r *http.Request) {
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		groups := s.callerGroupIDs(r)
		ids := make([]int64, 0, len(groups))
		for g := range groups {
			ids = append(ids, g)
		}
		items, err := ms.SharedItemsForGroups(r.Context(), ids, r.URL.Query().Get("kind"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []memory.SharedItem{}
		}
		writeJSON(w, map[string]interface{}{"items": items})

	case http.MethodPost:
		u := currentUser(r)
		if u == nil || u.ID == 0 {
			writeErr(w, http.StatusForbidden, "sharing requires a user account (multi-user)")
			return
		}
		var body struct {
			Kind     string `json:"kind"`
			GroupID  int64  `json:"groupId"`
			Session  string `json:"session"`
			WidgetID string `json:"widgetId"`
			Title    string `json:"title"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if !s.userInGroup(r.Context(), u.ID, body.GroupID) {
			writeErr(w, http.StatusForbidden, "you are not a member of that group")
			return
		}
		session, ok := s.sessionFor(r, body.Session)
		if !ok {
			writeErr(w, http.StatusForbidden, "not your session")
			return
		}
		kind := body.Kind
		only := ""
		if kind != "dashboard" {
			kind = "widget"
			only = body.WidgetID
			if only == "" {
				writeErr(w, http.StatusBadRequest, "widgetId required for a widget share")
				return
			}
		}
		widgets := s.sessionWidgets(session, only)
		if len(widgets) == 0 {
			writeErr(w, http.StatusNotFound, "nothing to share (widget not found or empty board)")
			return
		}
		title := strings.TrimSpace(body.Title)
		if title == "" {
			if kind == "widget" {
				title = widgets[0].Title
			} else {
				title = sessionDisplayName(body.Session) + " dashboard"
			}
		}
		payload, _ := json.Marshal(sharedPayload{Widgets: widgets})
		id, err := ms.ShareItem(r.Context(), memory.SharedItem{
			GroupID: body.GroupID, Kind: kind, Title: title,
			OwnerID: u.ID, OwnerName: u.DisplayName, Payload: payload,
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"id": id, "count": len(widgets)})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleSharedItem: POST /api/shared/<id>/add?session=<target>, DELETE /api/shared/<id>.
func (s *Server) handleSharedItem(w http.ResponseWriter, r *http.Request) {
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/shared/")
	idStr, action, _ := strings.Cut(rest, "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	item, ok, err := ms.SharedItemByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	u := currentUser(r)
	if u == nil || u.ID == 0 {
		writeErr(w, http.StatusForbidden, "sharing requires a user account (multi-user)")
		return
	}

	switch {
	case r.Method == http.MethodPost && action == "add":
		if !s.userInGroup(r.Context(), u.ID, item.GroupID) {
			writeErr(w, http.StatusForbidden, "you are not a member of that group")
			return
		}
		target, ok := s.sessionFor(r, r.URL.Query().Get("session"))
		if !ok {
			writeErr(w, http.StatusForbidden, "not your session")
			return
		}
		var p sharedPayload
		if json.Unmarshal(item.Payload, &p) != nil || len(p.Widgets) == 0 {
			writeErr(w, http.StatusInternalServerError, "shared item has no widgets")
			return
		}
		dir := filepath.Join(s.cfg.PluginDir, target)
		if err := os.MkdirAll(dir, 0755); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		added := 0
		for i, wdg := range p.Widgets {
			// Fresh id so it never collides with an existing widget on the board.
			wid := fmt.Sprintf("%s-shared%d-%d", shareSlug(wdg.Title), id, i)
			cols, height := wdg.Cols, wdg.Height
			if cols <= 0 {
				cols = 1
			}
			if height <= 0 {
				height = 280
			}
			if err := os.WriteFile(filepath.Join(dir, wid+".html"), []byte(wdg.Content), 0644); err != nil {
				continue
			}
			meta, _ := json.Marshal(map[string]interface{}{"title": wdg.Title, "cols": cols, "height": height})
			os.WriteFile(filepath.Join(dir, wid+".meta.json"), meta, 0644)
			s.pushPluginToSession(target, wid, wdg.Title, wdg.Content, cols, height)
			added++
		}
		writeJSON(w, map[string]interface{}{"added": added})

	case r.Method == http.MethodDelete:
		// The owner, a group admin, or a global admin may unpublish.
		allowed := item.OwnerID == u.ID || u.IsGlobalAdmin() || s.isGroupAdminOf(r.Context(), u, item.GroupID)
		if !allowed {
			writeErr(w, http.StatusForbidden, "only the owner or a group admin can unpublish")
			return
		}
		if err := ms.DeleteSharedItem(r.Context(), id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
