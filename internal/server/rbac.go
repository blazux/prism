package server

// RBAC enforcement (Prism heavy, Phase 2). Turns a user's identity + group
// memberships + the global tool policy into an agent.ToolGuard, and resolves a
// user's model allow-list. This generalises the per-sender guard first built for
// the Webex channel: personal agents (browser/WS) and /api/chat now run every
// tool call through the requesting user's permissions.
//
// Model:
//   - "admin" = global admin OR admin of any group. Admins bypass tool gating.
//   - Open by default: members may call any tool. This is a trusted internal
//     deployment — no hard floor, no admin-only default.
//   - A global admin can still mark individual tools admin-only via the tool
//     policy (Admin → Tools); group admins can tighten further for their group.

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"prism/internal/agent"
	"prism/internal/memory"
)

// This is a trusted, internal deployment: tools are fully open to members by
// default. Nothing is hard-floored and nothing defaults to admin-only. A global
// admin can still mark individual tools admin-only in the tool policy (Admin →
// Tools), and group admins can restrict further for their group — but out of the
// box every member gets the full agent (backend tools, MCP, docker, shell, …).
//
// To re-lock the powerful family if the trust model ever changes, add tool names
// back to alwaysAdminOnly (never openable) or defaultAdminOnly (openable default).
var alwaysAdminOnly = map[string]bool{}

// The outbound-call family is admin-only by default (a group admin can still open it
// to members via the tool policy): place_call dials real people and costs money,
// cancel_call can drop someone else's queued call, and list_calls exposes who is
// being called and why.
var defaultAdminOnly = map[string]bool{
	"place_call":  true,
	"list_calls":  true,
	"cancel_call": true,
}

// toolIsHardFloor reports whether a tool can never be opened to members. Empty by
// default; populate alwaysAdminOnly to bring the floor back.
func toolIsHardFloor(name string, args map[string]interface{}) bool {
	return alwaysAdminOnly[name]
}

// isAdminUser reports whether the user is a global admin or an admin of any group.
func (s *Server) isAdminUser(ctx context.Context, u *memory.User) bool {
	if u == nil {
		return false
	}
	if u.IsGlobalAdmin() {
		return true
	}
	if ms := s.store(); ms != nil {
		if ok, _ := ms.IsGroupAdminAnywhere(ctx, u.ID); ok {
			return true
		}
	}
	return false
}

// buildUserGuard returns the tool authorization gate for a user. A nil user
// (legacy/no-DB, or the internal service identity handled upstream) yields a nil
// guard = unrestricted, preserving single-owner behaviour.
func (s *Server) buildUserGuard(ctx context.Context, u *memory.User) agent.ToolGuard {
	if u == nil {
		return nil
	}
	// Admins are unrestricted — skip the per-call lookups entirely.
	if s.isAdminUser(ctx, u) {
		return nil
	}
	policies := s.toolPolicies(ctx)
	// Group restrictions tighten (never loosen) the global policy for this member.
	restricted := map[string]bool{}
	if ms := s.store(); ms != nil && u.ID > 0 {
		if r, err := ms.RestrictedToolsForUser(ctx, u.ID); err == nil {
			restricted = r
		}
	}
	return toolGuard(policies, restricted, u.Email)
}

// buildGroupAgentGuard returns the guard for a group's shared agent: the global
// policy plus that group's own restrictions, always at member level (the shared
// agent never gets admin-only tools unless the group opens them). This is the
// "group-admin-defined rights" model — independent of who mentioned the agent.
func (s *Server) buildGroupAgentGuard(ctx context.Context, groupID int64) agent.ToolGuard {
	policies := s.toolPolicies(ctx)
	restricted := map[string]bool{}
	if ms := s.store(); ms != nil {
		if gp, err := ms.GroupToolPolicies(ctx, groupID); err == nil {
			for tool, access := range gp {
				if access == "admin_only" || access == "disabled" {
					restricted[tool] = true
				}
			}
		}
	}
	return toolGuard(policies, restricted, "the shared agent")
}

func (s *Server) toolPolicies(ctx context.Context) map[string]string {
	if ms := s.store(); ms != nil {
		if p, err := ms.GetToolPolicies(ctx); err == nil {
			return p
		}
	}
	return map[string]string{}
}

// toolGuard builds the closure enforcing hard floor + global policy + extra
// per-group restrictions for a non-admin caller.
func toolGuard(policies map[string]string, restricted map[string]bool, who string) agent.ToolGuard {
	return func(name string, args map[string]interface{}) error {
		if toolIsHardFloor(name, args) {
			return fmt.Errorf("permission denied: %q is admin-only (it creates or alters shared tools); %s is not an admin", name, who)
		}
		access := policies[name]
		if access == "" {
			if defaultAdminOnly[name] {
				access = "admin_only"
			} else {
				access = "open"
			}
		}
		if access == "disabled" {
			return fmt.Errorf("tool %q has been disabled by the administrator on this platform", name)
		}
		if access == "admin_only" || restricted[name] {
			return fmt.Errorf("permission denied: tool %q is restricted; %s is not allowed to run it", name, who)
		}
		return nil
	}
}

// userCanUseModel reports whether a user may run the given chat model: the
// platform-wide allow-list (Admin → Platform) first, then per-group grants.
func (s *Server) userCanUseModel(ctx context.Context, u *memory.User, model string) bool {
	if u == nil || u.IsGlobalAdmin() || model == "" {
		return true
	}
	if set, unrestricted := s.platformAllowedModels(ctx); !unrestricted && !set[model] {
		return false
	}
	ms := s.store()
	if ms == nil {
		return true
	}
	set, unrestricted, err := ms.AllowedModelsForUser(ctx, u.ID)
	if err != nil || unrestricted {
		return true
	}
	return set[model]
}

// ragScopeFor returns the RAG tenant scope for a user: their primary group
// ("g<id>", shared with fellow members) or a personal scope ("u<id>") when they
// belong to no group. Empty for the service identity / legacy no-DB mode.
func (s *Server) ragScopeFor(ctx context.Context, u *memory.User) string {
	if u == nil || u.ID == 0 {
		return ""
	}
	if ms := s.store(); ms != nil {
		if groups, err := ms.UserGroups(ctx, u.ID); err == nil && len(groups) > 0 {
			return fmt.Sprintf("g%d", groups[0].GroupID)
		}
	}
	return fmt.Sprintf("u%d", u.ID)
}

// canManageRAGScope reports whether the user may MUTATE (upload/delete/describe)
// the RAG scope their operations resolve to. A group scope is admin-managed:
// members browse and search it read-only, the group admin curates it (the
// "admins configure RAG, users see what's available" principle). A personal
// scope (no group) is the user's own. Service identity / legacy stay unrestricted.
func (s *Server) canManageRAGScope(ctx context.Context, u *memory.User) bool {
	if u == nil || u.ID == 0 || u.IsGlobalAdmin() {
		return true
	}
	if ms := s.store(); ms != nil {
		if groups, err := ms.UserGroups(ctx, u.ID); err == nil && len(groups) > 0 {
			return groups[0].Role == memory.GroupRoleAdmin // scope = first group
		}
	}
	if s.cfg.MultiUser {
		return false // multi-user: no personal RAG scope is ever manageable
	}
	return true // personal scope (single-user only)
}

// ragPersonalFallbackBlocked reports whether scope is the personal RAG fallback
// that MULTI_USER retires: true only for a real multi-user account with no
// group (scope starts with "u", not "g"). Never true for "" (service identity
// / legacy single-user mode — unaffected), for a group scope, or for the
// reserved voiceGuestScope ("voice", voice.go) — that's the phone
// switchboard's own fixed knowledge base, not a personal fallback, and an
// admin can populate it (rag.go's ragScopeForRequest special-cases it) even
// in MULTI_USER mode; blocking reads here would make it write-only.
func (s *Server) ragPersonalFallbackBlocked(scope string) bool {
	if scope == voiceGuestScope {
		return false
	}
	return s.cfg.MultiUser && scope != "" && !strings.HasPrefix(scope, "g")
}

// scopeCollection / unscopeCollection delegate to agent.ScopeCollection /
// agent.UnscopeCollection — the same implementation the executor's own
// col/uncol use, so the RAG HTTP handlers and system-prompt context can never
// drift onto a different scoped-name format.
func scopeCollection(scope, name string) string   { return agent.ScopeCollection(scope, name) }
func unscopeCollection(scope, name string) string { return agent.UnscopeCollection(scope, name) }

// builtinToolNames lists the names of the built-in tools, for the admin policy UI.
func builtinToolNames() []string {
	names := make([]string, 0, len(agent.ToolDefinitions))
	for _, t := range agent.ToolDefinitions {
		if t.Function.Name != "" {
			names = append(names, t.Function.Name)
		}
	}
	return names
}

// filterModelsForUser trims a model list to those the user may use.
func (s *Server) filterModelsForUser(ctx context.Context, u *memory.User, models []string) []string {
	if u == nil || u.IsGlobalAdmin() {
		return models
	}
	ms := s.store()
	if ms == nil {
		return models
	}
	set, unrestricted, err := ms.AllowedModelsForUser(ctx, u.ID)
	if err != nil || unrestricted {
		return models
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		if set[m] {
			out = append(out, m)
		}
	}
	return out
}

// requireAdminUser gates the endpoints that hand out a shell in the tools
// container: /api/terminal (interactive PTY) and /api/exec (one-shot command).
// Both run as the container's user with the workspace mounted, so a plain member
// could read every group's files and the agent's secrets. Only a global admin or
// an admin of some group may reach them.
//
// A nil user means legacy single-owner mode (no database) or the internal service
// identity resolved upstream — unrestricted, exactly as buildUserGuard treats it.
// Returns false once it has written the 403.
func (s *Server) requireAdminUser(w http.ResponseWriter, r *http.Request) bool {
	u := currentUser(r)
	if u == nil || s.isAdminUser(r.Context(), u) {
		return true
	}
	http.Error(w, "forbidden: administrators only", http.StatusForbidden)
	return false
}
