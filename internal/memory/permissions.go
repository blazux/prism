package memory

// RBAC store layer (Prism heavy, Phase 2): group membership queries, global
// per-tool policy, and per-group model grants. The server (rbac.go) turns these
// into an agent.ToolGuard and a model allow-list.

import (
	"context"
	"errors"
	"fmt"
)

// Membership is one of a user's group memberships.
type Membership struct {
	GroupID   int64  `json:"groupId"`
	GroupName string `json:"groupName"`
	Role      string `json:"role"` // 'admin' | 'member'
}

// GroupMember is a member row within a group (admin view).
type GroupMember struct {
	UserID      int64  `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

// UserGroups returns every group the user belongs to, with their role in each.
func (s *Store) UserGroups(ctx context.Context, userID int64) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.name, gm.group_role
		FROM group_members gm JOIN groups g ON g.id = gm.group_id
		WHERE gm.user_id = $1 ORDER BY g.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.GroupID, &m.GroupName, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// IsGroupAdminAnywhere reports whether the user is an admin of any group.
func (s *Store) IsGroupAdminAnywhere(ctx context.Context, userID int64) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM group_members WHERE user_id = $1 AND group_role = 'admin'
	`, userID).Scan(&n)
	return n > 0, err
}

// GroupMembers lists the members of a group with their per-group role.
func (s *Store) GroupMembers(ctx context.Context, groupID int64) ([]GroupMember, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.email, u.display_name, gm.group_role
		FROM group_members gm JOIN users u ON u.id = gm.user_id
		WHERE gm.group_id = $1 ORDER BY u.display_name
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RemoveGroupMember(ctx context.Context, groupID, userID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	return err
}

func (s *Store) DeleteGroup(ctx context.Context, groupID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	return err
}

// PurgeGroupResidue deletes the rows a group owned that no FK covers: shared
// notes ("g<id>" scope), the shared-agent avatar, Webex config/token
// (":g<id>"-suffixed keys), and the room/Webex conversation histories.
// The exact-suffix patterns can't collide across ids ("…:g5" never matches
// "…:g55"). Best-effort — errors are ignored, leftovers are inert.
func (s *Store) PurgeGroupResidue(ctx context.Context, groupID int64) {
	scope := fmt.Sprintf("g%d", groupID)
	s.pool.Exec(ctx, `DELETE FROM notes WHERE session_id = $1`, scope)
	s.pool.Exec(ctx, `DELETE FROM avatars WHERE scope = $1`, "agent-"+scope)
	s.pool.Exec(ctx, `DELETE FROM agent_config WHERE key LIKE $1`, "%:"+scope)
	s.pool.Exec(ctx, `DELETE FROM secrets WHERE name LIKE $1`, "%:"+scope)
	s.pool.Exec(ctx, `DELETE FROM conversation_history WHERE session_id = $1 OR session_id LIKE $2`,
		"room-"+scope, "webex-"+scope+"-%")
	s.pool.Exec(ctx, `DELETE FROM agent_config WHERE key = $1 OR key LIKE $2`,
		"system_prompt_personality_room-"+scope, "system_prompt_personality_webex-"+scope+"-%")
}

// ─── tool policy ──────────────────────────────────────────────────────────────

// GetToolPolicies returns the explicit per-tool overrides (tool → access). Tools
// without a row fall back to the server's code default.
func (s *Store) GetToolPolicies(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT tool, access FROM tool_policy`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tool, access string
		if err := rows.Scan(&tool, &access); err != nil {
			return nil, err
		}
		out[tool] = access
	}
	return out, rows.Err()
}

// SetToolPolicy upserts a tool's access ('open' | 'admin_only').
func (s *Store) SetToolPolicy(ctx context.Context, tool, access string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tool_policy (tool, access) VALUES ($1, $2)
		ON CONFLICT (tool) DO UPDATE SET access = EXCLUDED.access
	`, tool, access)
	return err
}

// ─── per-group tool restrictions (Phase 5) ─────────────────────────────────────

// GroupToolPolicies returns a group's explicit tool restrictions (tool → access).
func (s *Store) GroupToolPolicies(ctx context.Context, groupID int64) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT tool, access FROM group_tool_policy WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tool, access string
		if err := rows.Scan(&tool, &access); err != nil {
			return nil, err
		}
		out[tool] = access
	}
	return out, rows.Err()
}

// SetGroupToolRestriction marks a tool admin_only for a group's members.
func (s *Store) SetGroupToolRestriction(ctx context.Context, groupID int64, tool string) error {
	return s.SetGroupToolAccess(ctx, groupID, tool, "admin_only")
}

// SetGroupToolAccess sets a group's access level for a tool: "admin_only"
// (members blocked) or "disabled" (nobody — not even admins or the shared
// agent). "open" is the absence of a row — use ClearGroupToolRestriction.
func (s *Store) SetGroupToolAccess(ctx context.Context, groupID int64, tool, access string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO group_tool_policy (group_id, tool, access) VALUES ($1, $2, $3)
		ON CONFLICT (group_id, tool) DO UPDATE SET access = EXCLUDED.access
	`, groupID, tool, access)
	return err
}

// ClearGroupToolRestriction removes a group's restriction (reverts to global policy).
func (s *Store) ClearGroupToolRestriction(ctx context.Context, groupID int64, tool string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM group_tool_policy WHERE group_id = $1 AND tool = $2`, groupID, tool)
	return err
}

// RestrictedToolsForUser returns the set of tools restricted (admin_only) by any
// of the user's groups — used to tighten the user's tool guard.
func (s *Store) RestrictedToolsForUser(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT gtp.tool
		FROM group_tool_policy gtp
		JOIN group_members gm ON gm.group_id = gtp.group_id
		WHERE gm.user_id = $1 AND gtp.access IN ('admin_only', 'disabled')
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var tool string
		if err := rows.Scan(&tool); err != nil {
			return nil, err
		}
		out[tool] = true
	}
	return out, rows.Err()
}

// ─── group model grants ───────────────────────────────────────────────────────

func (s *Store) GroupModels(ctx context.Context, groupID int64) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT model FROM group_models WHERE group_id = $1 ORDER BY model`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddGroupModel(ctx context.Context, groupID int64, model string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO group_models (group_id, model) VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, groupID, model)
	return err
}

func (s *Store) RemoveGroupModel(ctx context.Context, groupID int64, model string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM group_models WHERE group_id = $1 AND model = $2`, groupID, model)
	return err
}

// AllowedModelsForUser computes a user's model allow-list across their groups.
// Returns unrestricted=true when the user should see every model — i.e. they are
// in no group, or belong to at least one group that has no model restriction
// (zero grant rows). Otherwise `set` is the union of their groups' granted models.
func (s *Store) AllowedModelsForUser(ctx context.Context, userID int64) (set map[string]bool, unrestricted bool, err error) {
	groups, err := s.UserGroups(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	if len(groups) == 0 {
		return nil, true, nil
	}
	set = map[string]bool{}
	for _, g := range groups {
		models, err := s.GroupModels(ctx, g.GroupID)
		if err != nil {
			return nil, false, err
		}
		if len(models) == 0 {
			return nil, true, nil // a group with no restriction opens everything
		}
		for _, m := range models {
			set[m] = true
		}
	}
	return set, false, nil
}

// ── Default group ──────────────────────────────────────────────────────────────
//
// Group-scoped resources meet single users all the time: a phone rings, and the
// caller is a person, not a group. Rather than invent a rule per feature ("first
// match", "any admin group", "ask"), every such case asks the same question here.

// DefaultGroupID returns the group that speaks for this user, and whether they
// are in one at all.
//
// Their explicit choice wins, but only while they are still a member of it —
// otherwise the earliest group they belong to. That makes the common case (one
// group, no choice made) work with nothing to configure, and makes a removed
// membership or a deleted group degrade quietly instead of pointing at nothing.
func (s *Store) DefaultGroupID(ctx context.Context, userID int64) (int64, bool) {
	var chosen *int64
	if err := s.pool.QueryRow(ctx,
		`SELECT default_group_id FROM users WHERE id = $1`, userID).Scan(&chosen); err != nil {
		return 0, false
	}
	if chosen != nil {
		var ok bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM group_members WHERE user_id = $1 AND group_id = $2)`,
			userID, *chosen).Scan(&ok); err == nil && ok {
			return *chosen, true
		}
	}
	var first int64
	if err := s.pool.QueryRow(ctx,
		`SELECT group_id FROM group_members WHERE user_id = $1 ORDER BY group_id LIMIT 1`,
		userID).Scan(&first); err != nil {
		return 0, false
	}
	return first, true
}

// SetDefaultGroup records a user's choice. Passing 0 clears it, which returns
// them to the fallback above. A group they do not belong to is refused rather
// than stored and silently ignored later.
func (s *Store) SetDefaultGroup(ctx context.Context, userID, groupID int64) error {
	if groupID == 0 {
		_, err := s.pool.Exec(ctx, `UPDATE users SET default_group_id = NULL WHERE id = $1`, userID)
		return err
	}
	var member bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM group_members WHERE user_id = $1 AND group_id = $2)`,
		userID, groupID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return errors.New("not a member of that group")
	}
	_, err := s.pool.Exec(ctx, `UPDATE users SET default_group_id = $1 WHERE id = $2`, groupID, userID)
	return err
}
