package server

// Admin RBAC API (Prism heavy, Phase 2): groups + membership, global tool
// policy, and per-group model grants. All endpoints are global-admin only for
// now; delegating group management to group admins is a later refinement.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"prism/internal/memory"
)

func (s *Server) requireGlobalAdmin(w http.ResponseWriter, r *http.Request) *memory.User {
	u := currentUser(r)
	if u == nil || !u.IsGlobalAdmin() {
		writeErr(w, http.StatusForbidden, "global admin only")
		return nil
	}
	return u
}

// purgeGroupResidue removes everything a deleted group owned outside the FK
// cascades (members/policies/room rows cascade in SQL). Best-effort: each item
// is independent, and a failure only leaves inert orphans behind.
func (s *Server) purgeGroupResidue(groupID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope := fmt.Sprintf("g%d", groupID)

	// RAG collections (stored as "g<id>--<name>").
	if s.ragStore != nil {
		if cols, err := s.ragStore.ListCollections(ctx, scope); err == nil {
			for _, c := range cols {
				s.ragStore.DeleteCollection(ctx, c.Name)
			}
		}
	}
	// Group MCP servers (session "g<id>") — via the manager so live clients close.
	if s.mcpMgr != nil {
		if servers, err := s.mcpMgr.List(ctx, scope); err == nil {
			for _, srv := range servers {
				s.mcpMgr.RemoveByID(ctx, scope, srv.ID)
			}
		}
	}
	if ms := s.store(); ms != nil {
		ms.PurgeGroupResidue(ctx, groupID)
	}
	// Stop a Webex bot that was running for this group.
	s.startChannels()
	log.Printf("[admin] purged residue of deleted group g%d", groupID)
}

// groupView bundles a group with its members for the admin UI.
type groupView struct {
	memory.Group
	Members []memory.GroupMember `json:"members"`
	Models  []string             `json:"models"`
}

// GET /api/admin/groups → groups with members + model grants.
// POST actions: create|delete|add_member|remove_member|set_role.
func (s *Server) handleAdminGroups(w http.ResponseWriter, r *http.Request) {
	if s.requireGlobalAdmin(w, r) == nil {
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	switch r.Method {
	case http.MethodGet:
		groups, err := ms.ListGroups(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]groupView, 0, len(groups))
		for _, g := range groups {
			members, _ := ms.GroupMembers(r.Context(), g.ID)
			models, _ := ms.GroupModels(r.Context(), g.ID)
			views = append(views, groupView{Group: g, Members: members, Models: models})
		}
		writeJSON(w, map[string]any{"groups": views})
	case http.MethodPost:
		var b struct {
			Action  string `json:"action"`
			Name    string `json:"name"`
			GroupID int64  `json:"groupId"`
			UserID  int64  `json:"userId"`
			Role    string `json:"role"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		s.audit(r, "group_"+b.Action, map[string]interface{}{"name": b.Name, "groupId": b.GroupID, "userId": b.UserID, "role": b.Role})
		var err error
		switch b.Action {
		case "create":
			if b.Name == "" {
				writeErr(w, http.StatusBadRequest, "name required")
				return
			}
			_, err = ms.CreateGroup(r.Context(), b.Name)
		case "delete":
			err = ms.DeleteGroup(r.Context(), b.GroupID)
			if err == nil {
				// Best-effort purge of everything keyed by the group outside the FK
				// cascades: RAG collections, group MCP servers, Webex config/token,
				// shared notes, agent avatar, room/Webex histories.
				go s.purgeGroupResidue(b.GroupID)
			}
		case "add_member", "set_role":
			role := b.Role
			if role != memory.GroupRoleAdmin {
				role = memory.GroupRoleMember
			}
			// The whole scoping model (RAG, notes, MCP, room) hangs off a user's
			// single "primary" group — belonging to two would silently flip scopes.
			// Enforce one group per user: move them explicitly instead.
			if groups, gerr := ms.UserGroups(r.Context(), b.UserID); gerr == nil {
				for _, g := range groups {
					if g.GroupID != b.GroupID {
						writeErr(w, http.StatusConflict, "this user already belongs to group \""+g.GroupName+"\" — remove them from it first (one group per user)")
						return
					}
				}
			}
			err = ms.AddGroupMember(r.Context(), b.GroupID, b.UserID, role)
		case "remove_member":
			err = ms.RemoveGroupMember(r.Context(), b.GroupID, b.UserID)
		default:
			writeErr(w, http.StatusBadRequest, "unknown action")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// GET /api/admin/tool-policy → tool list + current effective access.
// POST {tool, access:'open'|'admin_only'} to override.
func (s *Server) handleAdminToolPolicy(w http.ResponseWriter, r *http.Request) {
	if s.requireGlobalAdmin(w, r) == nil {
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	switch r.Method {
	case http.MethodGet:
		overrides, _ := ms.GetToolPolicies(r.Context())
		type row struct {
			Tool      string `json:"tool"`
			Access    string `json:"access"`
			HardFloor bool   `json:"hardFloor"`
		}
		var rows []row
		for _, name := range builtinToolNames() {
			access := overrides[name]
			if access == "" {
				if defaultAdminOnly[name] {
					access = "admin_only"
				} else {
					access = "open"
				}
			}
			rows = append(rows, row{Tool: name, Access: access, HardFloor: alwaysAdminOnly[name] || name == "mcp"})
		}
		writeJSON(w, map[string]any{"tools": rows})
	case http.MethodPost:
		var b struct{ Tool, Access string }
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Tool == "" {
			writeErr(w, http.StatusBadRequest, "tool and access required")
			return
		}
		if b.Access != "open" && b.Access != "admin_only" && b.Access != "disabled" {
			writeErr(w, http.StatusBadRequest, "access must be 'open', 'admin_only' or 'disabled'")
			return
		}
		if err := ms.SetToolPolicy(r.Context(), b.Tool, b.Access); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, "tool_policy", map[string]interface{}{"tool": b.Tool, "access": b.Access})
		writeJSON(w, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// GET/POST /api/group/tool-policy?group=<id> — per-group tool restrictions,
// managed by that group's admin (Phase 5). A group admin may only tighten
// (restrict an otherwise-open tool for their members), never loosen: tools that
// are already admin-only globally, or in the hard floor, are locked.
func (s *Server) handleGroupToolPolicy(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return
	}
	if !s.isGroupAdminOf(r.Context(), u, groupID) {
		writeErr(w, http.StatusForbidden, "group admin only")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	switch r.Method {
	case http.MethodGet:
		global, _ := ms.GetToolPolicies(r.Context())
		grp, _ := ms.GroupToolPolicies(r.Context(), groupID)
		type row struct {
			Tool            string `json:"tool"`
			GlobalAdminOnly bool   `json:"globalAdminOnly"` // locked: can't be loosened by the group
			HardFloor       bool   `json:"hardFloor"`
			GroupRestricted bool   `json:"groupRestricted"`
		}
		var rows []row
		for _, name := range builtinToolNames() {
			access := global[name]
			if access == "" {
				if defaultAdminOnly[name] {
					access = "admin_only"
				} else {
					access = "open"
				}
			}
			rows = append(rows, row{
				Tool:            name,
				GlobalAdminOnly: access == "admin_only" || access == "disabled",
				HardFloor:       alwaysAdminOnly[name] || name == "mcp",
				GroupRestricted: grp[name] == "admin_only",
			})
		}
		writeJSON(w, map[string]any{"tools": rows})
	case http.MethodPost:
		var b struct {
			Tool     string `json:"tool"`
			Restrict *bool  `json:"restrict"`         // legacy toggle (access pane)
			Access   string `json:"access,omitempty"` // "open" | "admin_only" | "disabled"
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Tool == "" {
			writeErr(w, http.StatusBadRequest, "tool required")
			return
		}
		access := b.Access
		if access == "" && b.Restrict != nil {
			if *b.Restrict {
				access = "admin_only"
			} else {
				access = "open"
			}
		}
		switch access {
		case "open":
			err = ms.ClearGroupToolRestriction(r.Context(), groupID, b.Tool)
		case "admin_only", "disabled":
			err = ms.SetGroupToolAccess(r.Context(), groupID, b.Tool, access)
		default:
			writeErr(w, http.StatusBadRequest, "access must be open, admin_only or disabled")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, "group_tool_access", map[string]interface{}{"groupId": groupID, "tool": b.Tool, "access": access})
		writeJSON(w, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// POST /api/admin/group-models {groupId, model, action:'add'|'remove'}
func (s *Server) handleAdminGroupModels(w http.ResponseWriter, r *http.Request) {
	if s.requireGlobalAdmin(w, r) == nil {
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	var b struct {
		GroupID int64  `json:"groupId"`
		Model   string `json:"model"`
		Action  string `json:"action"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.GroupID == 0 || b.Model == "" {
		writeErr(w, http.StatusBadRequest, "groupId, model, action required")
		return
	}
	var err error
	switch b.Action {
	case "add":
		err = ms.AddGroupModel(r.Context(), b.GroupID, b.Model)
	case "remove":
		err = ms.RemoveGroupModel(r.Context(), b.GroupID, b.Model)
	default:
		writeErr(w, http.StatusBadRequest, "action must be add|remove")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
