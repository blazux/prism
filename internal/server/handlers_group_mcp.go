package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"prism/internal/mcp"
)

// Group-shared MCP servers. A group admin connects servers under the synthetic
// session id "g<id>" (the group's RAG scope); the executor merges that layer
// into every member session and the shared agent (see agent.AllDynamicTools),
// so one admin-managed list serves the whole group. Members may read the list
// (their settings show it read-only); only group admins mutate it.
//
// GET    /api/group/mcp?group=<id>            → {servers}
// POST   /api/group/mcp?group=<id>            → {name,url,authSecret} connect
// DELETE /api/group/mcp?group=<id>&id=<srvID> → remove
func (s *Server) handleGroupMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	u := currentUser(r)
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return
	}
	scope := fmt.Sprintf("g%d", groupID)

	switch r.Method {
	case http.MethodGet:
		if !u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, groupID) {
			writeErr(w, http.StatusForbidden, "not a member")
			return
		}
		servers, err := s.mcpMgr.List(r.Context(), scope)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if servers == nil {
			servers = []mcp.ServerConfig{}
		}
		// Per-tool access levels (open unless overridden), for the admin UI.
		policy := map[string]string{}
		if ms := s.store(); ms != nil {
			if gp, err := ms.GroupToolPolicies(r.Context(), groupID); err == nil && gp != nil {
				policy = gp
			}
		}
		writeJSON(w, map[string]interface{}{"servers": servers, "policy": policy})

	case http.MethodPost:
		if !s.isGroupAdminOf(r.Context(), u, groupID) {
			writeErr(w, http.StatusForbidden, "group admin only")
			return
		}
		var b struct {
			Name       string `json:"name"`
			URL        string `json:"url"`
			AuthSecret string `json:"authSecret"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Name == "" || b.URL == "" {
			writeErr(w, http.StatusBadRequest, "name and url required")
			return
		}
		tools, err := s.mcpMgr.Connect(r.Context(), scope, b.Name, b.URL, b.AuthSecret)
		if err != nil {
			// The server wants OAuth: run discovery + registration and hand the UI a
			// consent URL to open, same as the personal MCP flow (handleMCPServers).
			var needAuth *mcp.OAuthRequiredError
			if errors.As(err, &needAuth) {
				redirectURI := externalBase(r) + "/api/oauth/mcp/callback"
				authURL, state, serr := s.mcpMgr.StartOAuth(r.Context(), scope, b.Name, b.URL, needAuth.ResourceMetadata, redirectURI)
				if serr != nil {
					writeErr(w, http.StatusInternalServerError, "oauth setup: "+serr.Error())
					return
				}
				writeJSON(w, map[string]interface{}{
					"needs_oauth":   true,
					"authorize_url": authURL,
					"state":         state,
				})
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "count": len(tools)})

	case http.MethodDelete:
		if !s.isGroupAdminOf(r.Context(), u, groupID) {
			writeErr(w, http.StatusForbidden, "group admin only")
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErr(w, http.StatusBadRequest, "id required")
			return
		}
		if err := s.mcpMgr.RemoveByID(r.Context(), scope, id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
