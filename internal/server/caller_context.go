package server

import (
	"context"
	"fmt"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

// CallerContext bundles the four independent pieces every entry point needs
// to configure a ToolExecutor correctly: the permission guard, the RAG/MCP
// tenant scope, the personal-knowledge scope override (if any), and the
// hidden-tools set. Before this, every entry point (WS chat, /api/chat,
// /api/builtin, Telegram, Webex, rooms, voice) assembled these by hand with
// its own sequence of Set* calls — which is exactly how two real bugs shipped
// the same day: a caller correctly wired 3 of the 4 pieces and silently
// missed the 4th (personal MCP scope tied to the wrong board; group RAG/MCP
// scope never resolved for service-token self-calls). Bundling them into one
// value with one apply step makes "forgot a field" structurally harder.
type CallerContext struct {
	MutationGate  func() error
	Guard         agent.ToolGuard
	RAGScope      string
	PersonalScope string // "" = let the executor derive it from the session id itself
	HiddenTools   map[string]bool
	MultiUser     bool                             // MULTI_USER: retires the personal RAG/MCP fallback in the executor
	Groups        []memory.Membership              // groups this caller can share widgets within
	ActingUserID  int64                            // the real user behind the session (share ownership)
	Help          agent.HelpFn                     // Prism's bundled docs for prism_help
	Status        func(ctx context.Context) string // what this caller has configured
	ServerTools   map[string]agent.ServerTool      // webhook / pim_source / channel implementations (nil = unavailable)
	GlobalAdmin   bool                             // may manage any group's MCP servers
	RAGReadOnly   bool                             // group knowledge base: search only (not its admin)
}

// apply sets every field this bundles onto an executor in one call, instead
// of the 3-6 individual Set* calls each entry point used to repeat by hand.
func (cc CallerContext) apply(e *agent.ToolExecutor) {
	e.SetResourceMutationGuard(cc.MutationGate)
	e.SetToolGuard(cc.Guard)
	e.SetRAGScope(cc.RAGScope)
	if cc.PersonalScope != "" {
		e.SetPersonalScope(cc.PersonalScope)
	}
	e.SetHiddenTools(cc.HiddenTools)
	e.SetMultiUserMode(cc.MultiUser)
	e.SetSharingContext(cc.ActingUserID, cc.Groups)
	e.SetHelp(cc.Help, cc.Status)
	e.SetServerTools(cc.ServerTools)
	e.SetGlobalAdmin(cc.GlobalAdmin)
	e.SetRAGReadOnly(cc.RAGReadOnly)
}

// callerContextForUser builds the standard per-user CallerContext — the
// shape used by /api/chat, /api/builtin, Telegram, and Webex DMs. u is the
// caller's authenticated identity, which nil or the service identity
// (id 0 — a Bearer PRISM_TOKEN self-call from a cron job, custom tool or
// widget) leaves unrestricted (guard=nil), matching this deployment's
// trusted-internal-tool model.
//
// Even for a service-token call, sessionID usually still encodes which real
// user this is ("u<id>-<board>") — resolve THAT user's RAG/MCP scope (their
// group, if any) rather than defaulting to the anonymous identity's empty
// scope, so a group-shared MCP server or RAG collection stays reachable from
// self-calls. This does not touch the permission guard, which stays
// unrestricted for service calls by design.
func (s *Server) callerContextForUser(ctx context.Context, u *memory.User, sessionID string) CallerContext {
	if u != nil && u.ID < 0 {
		return s.callerContextForGroup(ctx, -u.ID)
	}
	if fresh, err := s.freshUser(ctx, u); err != nil {
		return CallerContext{
			Guard:        func(string, map[string]interface{}) error { return err },
			MutationGate: func() error { return err },
			MultiUser:    s.cfg.MultiUser,
		}
	} else {
		u = fresh
	}
	scopeUser := u
	if scopeUser == nil || scopeUser.ID == 0 {
		if uid := userIDFromSessionID(sessionID); uid > 0 {
			scopeUser = &memory.User{ID: uid}
		}
	}
	scope := s.ragScopeFor(ctx, scopeUser)
	if scope == "" {
		// Still no scope: this may be a service-token self-call into a
		// SHARED-agent session (cron/widget/custom-tool hitting /api/chat or
		// /api/builtin with a "webex-g<id>-…" or "room-g<id>" session, not a
		// personal "u<id>-…" one) — recover the group scope the session id
		// itself encodes, so its group-scoped RAG/MCP stays reachable. See
		// groupScopeFromSessionID's doc comment.
		if gscope := groupScopeFromSessionID(sessionID); gscope != "" {
			scope = gscope
		}
	}
	var groups []memory.Membership
	if ms := s.store(); ms != nil && scopeUser != nil && scopeUser.ID > 0 {
		groups, _ = ms.UserGroups(ctx, scopeUser.ID)
	}
	aid := int64(0)
	personalScope := ""
	if scopeUser != nil {
		aid = scopeUser.ID
		if aid > 0 {
			personalScope = fmt.Sprintf("u%d", aid)
		}
	}
	return CallerContext{
		MutationGate: func() error {
			s.runsMu.Lock()
			blocked := s.deletingSessions[sessionID]
			s.runsMu.Unlock()
			if blocked {
				return fmt.Errorf("workspace is being deleted or has been deleted; switch to an existing workspace")
			}
			return nil
		},
		Guard:         s.revocableUserGuard(u, groups, s.buildUserGuard(ctx, u)),
		RAGScope:      scope,
		PersonalScope: personalScope,
		HiddenTools:   s.hiddenToolsFor(ctx, sessionID, scope),
		MultiUser:     s.cfg.MultiUser,
		Groups:        groups,
		ActingUserID:  aid,
		Help:          s.helpFn(),
		Status:        s.integrationsStatusFor(scopeUser),
		ServerTools:   s.serverToolsFor(scopeUser),
		GlobalAdmin:   u != nil && u.IsGlobalAdmin(),
		RAGReadOnly:   !s.canManageRAGScope(ctx, scopeUser),
	}
}

// A server-owned turn survives a normal browser disconnect. Account revocation
// must still stop its next tool call, including headless/channel turns. Keep
// this lookup independent of the original HTTP/socket context and bounded.
func (s *Server) revocableUserGuard(user *memory.User, groups []memory.Membership, guard agent.ToolGuard) agent.ToolGuard {
	if user == nil || user.ID <= 0 {
		return guard
	}
	uid, role := user.ID, user.Role
	permissions := make(map[int64]string, len(groups))
	for _, group := range groups {
		permissions[group.GroupID] = group.Role
	}
	return func(name string, args map[string]interface{}) error {
		ctx, cancel := context.WithTimeout(s.runtimeContext(), 3*time.Second)
		defer cancel()
		fresh, err := s.freshUser(ctx, &memory.User{ID: uid})
		if err != nil {
			return err
		}
		if fresh.Role != role {
			return fmt.Errorf("account permissions changed; start a new task")
		}
		ms := s.store()
		if ms == nil {
			return fmt.Errorf("account permissions unavailable")
		}
		current, err := ms.UserGroups(ctx, uid)
		if err != nil {
			return fmt.Errorf("account permissions unavailable")
		}
		if len(current) != len(permissions) {
			return fmt.Errorf("group permissions changed; start a new task")
		}
		for _, group := range current {
			if permissions[group.GroupID] != group.Role {
				return fmt.Errorf("group permissions changed; start a new task")
			}
		}
		if guard != nil {
			return guard(name, args)
		}
		return nil
	}
}

// callerContextForGroup builds the CallerContext for a group's shared agent
// (room chat, Webex space): the group-admin-defined tool policy, the group's
// RAG/MCP scope, no personal-knowledge scope (the group itself is "the user"
// this agent learns about — see executor.personalScope's own doc comment),
// and a tool catalogue matching that same name-based group policy.
func (s *Server) callerContextForGroup(ctx context.Context, groupID int64) CallerContext {
	guard := s.buildGroupAgentGuard(ctx, groupID)
	hidden := s.hiddenToolsFor(ctx, fmt.Sprintf("room-g%d", groupID), fmt.Sprintf("g%d", groupID))
	// Group policy depends on tool names, so hide denied names before the model
	// chooses a tool. Execution still applies the same guard.
	hideDenied := func(name string) {
		if guard(name, nil) != nil {
			hidden[name] = true
		}
	}
	for _, tool := range agent.ToolDefinitions {
		hideDenied(tool.Function.Name)
	}
	for _, tool := range agent.OutboundCallTools {
		hideDenied(tool.Function.Name)
	}
	return CallerContext{
		Guard:       guard,
		HiddenTools: hidden,
		RAGScope:    fmt.Sprintf("g%d", groupID),
		MultiUser:   s.cfg.MultiUser,
		Groups:      []memory.Membership{{GroupID: groupID, Role: "member"}},
		Help:        s.helpFn(),
		Status:      s.groupStatusFor(groupID),
	}
}

// withSenderGate further restricts a group CallerContext's guard with an
// additional per-sender check — Webex spaces gate on both the group's tool
// policy AND a per-sender allowlist, unlike a room's shared agent which only
// has the first gate.
func (cc CallerContext) withSenderGate(gate agent.ToolGuard) CallerContext {
	base := cc.Guard
	cc.Guard = func(name string, args map[string]interface{}) error {
		if err := base(name, args); err != nil {
			return err
		}
		return gate(name, args)
	}
	return cc
}

// trustedCallerContext is the "no Prism identity to resolve a guard from"
// case: a zero CallerContext is guard=nil (unrestricted), scope="" (no
// group/RAG), no personal scope, nothing hidden — exactly single-owner
// Prism's own trust model. Used by Slack: its trust-on-first-use owner is a
// bare Slack user id with no link to any Prism account/role (see slack.go),
// so there is nothing to resolve a real guard from. This is the same
// behavior the bare nil/"" literal at the call site had before — expressed
// through the shared type so it reads as a deliberate choice, not a special
// case that's easy to mistake for an oversight.
func trustedCallerContext() CallerContext { return CallerContext{} }
