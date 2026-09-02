package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"prism/internal/agent"
)

// Group-shared secrets: credentials the whole group uses — an API key for the
// group's MCP servers, or a shared account's login a member contributed (the
// Settings "group secret" checkbox posts here via /api/user/secrets). Stored
// under ConfigScope("g<id>") — isolated from every other group and from the
// legacy global bucket (/api/secrets). Group secrets are shared by design:
// every member's agent receives them (ToolExecutor.secretsEnv), so members may
// list AND mutate them — except reserved integration names (email, OAuth, MCP
// tokens), which stay group-admin-only like handleGroupMCP.
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

	// canMutate: group admins always; plain members for non-reserved names
	// only (a member contributes/rotates a shared credential, but can't touch
	// the group's built-in integration secrets).
	canMutate := func(name string) bool {
		if s.isGroupAdminOf(r.Context(), u, groupID) {
			return true
		}
		return s.userInGroup(r.Context(), u.ID, groupID) && !agent.IsReservedSecretName(name)
	}

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
		var b struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Name == "" || b.Value == "" {
			writeErr(w, http.StatusBadRequest, "name and value required")
			return
		}
		if !canMutate(b.Name) {
			writeErr(w, http.StatusForbidden, "group admin only for this name")
			return
		}
		if err := scoped.SetSecret(r.Context(), b.Name, b.Value); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			writeErr(w, http.StatusBadRequest, "name required")
			return
		}
		if !canMutate(name) {
			writeErr(w, http.StatusForbidden, "group admin only for this name")
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
