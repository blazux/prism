package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// Group-shared secrets: an API key or bearer token a group admin manages for
// the group's MCP servers / group tools. Stored under ConfigScope("g<id>") —
// isolated from every other group and from the legacy global bucket
// (/api/secrets). Members may list names (read-only in their Settings); only
// group admins mutate. Mirrors handleGroupMCP's gating exactly.
//
// GET    /api/group/secrets?group=<id>              → {secrets: [name,...]}
// POST   /api/group/secrets?group=<id>              → {name,value} create/update
// DELETE /api/group/secrets?group=<id>&name=<name>  → delete
func (s *Server) handleGroupSecrets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	u := currentUser(r)
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "memory store not available")
		return
	}
	scoped := ms.ConfigScope(fmt.Sprintf("g%d", groupID))

	switch r.Method {
	case http.MethodGet:
		if !u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, groupID) {
			writeErr(w, http.StatusForbidden, "not a member")
			return
		}
		names, err := scoped.ListScopedSecretNames(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if names == nil {
			names = []string{}
		}
		writeJSON(w, map[string]interface{}{"secrets": names})

	case http.MethodPost:
		if !s.isGroupAdminOf(r.Context(), u, groupID) {
			writeErr(w, http.StatusForbidden, "group admin only")
			return
		}
		var b struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Name == "" || b.Value == "" {
			writeErr(w, http.StatusBadRequest, "name and value required")
			return
		}
		if err := scoped.SetSecret(r.Context(), b.Name, b.Value); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})

	case http.MethodDelete:
		if !s.isGroupAdminOf(r.Context(), u, groupID) {
			writeErr(w, http.StatusForbidden, "group admin only")
			return
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			writeErr(w, http.StatusBadRequest, "name required")
			return
		}
		if err := scoped.DeleteSecret(r.Context(), name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
