package server

// Per-tool access for group MCP tools (Spectrum).
//
// Two layers stack:
//   - the group admin's access level per tool (group_tool_policy):
//     open | admin_only | disabled — configured in Admin → MCP;
//   - the user's own opt-outs (Settings → Tools): a member may hide an open
//     tool from their personal agent ("u<id>:hidden_group_tools" JSON array).
//
// hiddenToolsFor resolves both into the executor's hidden set: hidden tools are
// removed from the model's tool list and denied at Execute.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"prism/internal/memory"
)

const keyHiddenGroupTools = "hidden_group_tools"

// hiddenToolsFor computes the tools a session must not see: the group's
// disabled tools (everyone, including admins and the shared agent), admin-only
// tools when the session owner is not a group admin (shared agents count as
// members), and the owner's personal opt-outs.
func (s *Server) hiddenToolsFor(ctx context.Context, sessionID, ragScope string) map[string]bool {
	hidden := map[string]bool{}
	ms := s.store()
	if ms == nil {
		return hidden
	}
	// Globally disabled tools (Admin → Tools) vanish for everyone — admins and
	// the shared agent included; that's what distinguishes them from admin_only.
	for tool, access := range s.toolPolicies(ctx) {
		if access == "disabled" {
			hidden[tool] = true
		}
	}
	uid := memory.SessionOwnerID(sessionID)
	if strings.HasPrefix(ragScope, "g") {
		if gid, err := strconv.ParseInt(ragScope[1:], 10, 64); err == nil && gid > 0 {
			isAdmin := false
			if uid > 0 {
				if u, err := ms.GetUserByID(ctx, uid); err == nil && u != nil {
					isAdmin = s.isGroupAdminOf(ctx, u, gid)
				}
			}
			if gp, err := ms.GroupToolPolicies(ctx, gid); err == nil {
				for tool, access := range gp {
					if access == "disabled" || (access == "admin_only" && !isAdmin) {
						hidden[tool] = true
					}
				}
			}
		}
	}
	if uid > 0 {
		us := ms.ConfigScope(fmt.Sprintf("u%d", uid))
		if raw, ok, _ := us.GetConfig(ctx, keyHiddenGroupTools); ok && raw != "" {
			var list []string
			if json.Unmarshal([]byte(raw), &list) == nil {
				for _, t := range list {
					hidden[t] = true
				}
			}
		}
	}
	return hidden
}

// GET /api/my/tool-prefs → the group MCP tools available to the caller, with
// the admin's access level and the caller's own hidden flag.
// POST {tool, hidden} → toggle a personal opt-out.
func (s *Server) handleMyToolPrefs(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	us := s.userStore(r)

	readHidden := func() map[string]bool {
		set := map[string]bool{}
		if raw, ok, _ := us.GetConfig(r.Context(), keyHiddenGroupTools); ok && raw != "" {
			var list []string
			if json.Unmarshal([]byte(raw), &list) == nil {
				for _, t := range list {
					set[t] = true
				}
			}
		}
		return set
	}

	switch r.Method {
	case "GET":
		type toolRow struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Server      string `json:"server"`
			Access      string `json:"access"` // open | admin_only | disabled
			Hidden      bool   `json:"hidden"` // user's own opt-out
		}
		rows := []toolRow{}
		groups, _ := ms.UserGroups(r.Context(), u.ID)
		if len(groups) > 0 {
			gid := groups[0].GroupID
			scope := fmt.Sprintf("g%d", gid)
			policy, _ := ms.GroupToolPolicies(r.Context(), gid)
			hidden := readHidden()
			servers, _ := s.mcpMgr.List(r.Context(), scope)
			for _, srv := range servers {
				if !srv.Enabled {
					continue
				}
				for _, t := range srv.Tools {
					access := policy[t.Name]
					if access == "" {
						access = "open"
					}
					rows = append(rows, toolRow{Name: t.Name, Description: t.Description, Server: srv.Name, Access: access, Hidden: hidden[t.Name]})
				}
			}
		}
		writeJSON(w, map[string]interface{}{"tools": rows})
	case "POST":
		var b struct {
			Tool   string `json:"tool"`
			Hidden bool   `json:"hidden"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Tool == "" {
			http.Error(w, "tool required", 400)
			return
		}
		set := readHidden()
		if b.Hidden {
			set[b.Tool] = true
		} else {
			delete(set, b.Tool)
		}
		list := make([]string, 0, len(set))
		for t := range set {
			list = append(list, t)
		}
		data, _ := json.Marshal(list)
		if err := us.SetConfig(r.Context(), keyHiddenGroupTools, string(data)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
