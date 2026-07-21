package server

// Per-user session scoping (Prism heavy, Phase 3). Every session a user touches
// is stored under an id prefixed with their user id ("u<id>-<name>"). The prefix
// is derived from the authenticated identity, never from client input, so a user
// cannot address another user's sessions — isolation by construction, with no
// per-query owner filter to forget. All per-session data (history, notifications,
// personality, plugins, per-session RAG) inherits the isolation transitively.
//
// The internal service identity (id 0) and legacy/no-DB mode use ids unchanged,
// preserving single-user behaviour.

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"prism/internal/memory"
)

var userPrefixRe = regexp.MustCompile(`^u\d+-`)
var userIDPrefixRe = regexp.MustCompile(`^u\d+`)

// personalScopeFromSessionID extracts the stable per-user scope ("u<id>") from
// a board session id ("u<id>-<board>"), or "" if the id carries no user
// prefix (legacy/service-identity sessions). Mirrors the agent's own
// personalScope() (internal/agent/executor.go) so server-side code that only
// has the session id string — not an authenticated *memory.User — can still
// resolve where that user's personal MCP servers live.
func personalScopeFromSessionID(sessionID string) string {
	if m := userIDPrefixRe.FindString(sessionID); m != "" && strings.HasPrefix(sessionID, m+"-") {
		return m
	}
	return ""
}

// userIDFromSessionID extracts the numeric id from a "u<id>-<board>" session,
// or 0 if the session carries no user prefix. Lets a caller resolve which
// real user a server-side self-call (Bearer PRISM_TOKEN, i.e. the anonymous
// service identity — see auth.go's serviceUser) is acting on behalf of, so
// group-shared resources (RAG collections, group MCP servers) scoped to that
// user's group stay reachable from cron/custom-tool/widget calls instead of
// silently resolving to the service identity's empty scope.
func userIDFromSessionID(sessionID string) int64 {
	scope := personalScopeFromSessionID(sessionID)
	if scope == "" {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(scope, "u"), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// ownerPtr returns the storage owner id for a user: nil for the service identity
// (id 0) or a nil user, else a pointer to the user id.
func ownerPtr(u *memory.User) *int64 {
	if u == nil || u.ID == 0 {
		return nil
	}
	id := u.ID
	return &id
}

// sessionFor maps a client-facing session id to its per-user storage id.
// Returns ok=false when the client tries to reach a different user's namespace.
func (s *Server) sessionFor(r *http.Request, clientID string) (string, bool) {
	id := sanitizeSessionID(clientID)
	if id == "" {
		id = "default"
	}
	u := currentUser(r)
	if u == nil || u.ID == 0 {
		return id, true // legacy / internal service identity: no namespacing
	}
	mine := fmt.Sprintf("u%d-", u.ID)
	if strings.HasPrefix(id, mine) {
		return id, true // already this user's namespace (idempotent round-trip)
	}
	if userPrefixRe.MatchString(id) {
		return "", false // a different user's namespace — refuse
	}
	return mine + id, true
}

// sessionDisplayName derives a friendly name for a newly created session from the
// client id ("default" → "Main").
func sessionDisplayName(clientID string) string {
	c := sanitizeSessionID(clientID)
	if c == "" || c == "default" {
		return "Main"
	}
	return clientID
}
