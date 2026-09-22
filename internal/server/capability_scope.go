package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"prism/internal/memory"
)

type capabilityScopeKey struct{}

func boundSession(r *http.Request) string {
	session, _ := r.Context().Value(capabilityScopeKey{}).(string)
	return session
}

// Existing signed group tokens remain valid, but no longer confer the service
// identity. Group authority is reconstructed from the signed session only.
func (s *Server) capabilityRequest(r *http.Request, uid int64, session string) (*http.Request, bool) {
	var u *memory.User
	if s.cfg.MultiUser && uid == 0 {
		scope := groupScopeFromSessionID(session)
		if scope == "" {
			return nil, false
		}
		gid, err := strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
		if err != nil || gid <= 0 {
			return nil, false
		}
		ms := s.store()
		if ms == nil {
			return nil, false
		}
		groups, err := ms.ListGroups(r.Context())
		if err != nil {
			return nil, false
		}
		found := false
		for _, g := range groups {
			if g.ID == gid {
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
		// Negative ids are in-process principals, never persisted accounts.
		u = &memory.User{ID: -gid, Role: memory.RoleMember, Status: memory.StatusApproved, Email: fmt.Sprintf("group:%d", gid)}
		if !groupCapabilityPath(r) {
			return nil, false
		}
	} else {
		u = s.userForCapToken(r.Context(), uid)
		if u == nil {
			return nil, false
		}
	}
	if requested := r.URL.Query().Get("session"); requested != "" && requested != session {
		return nil, false
	}
	r = withUser(r, u)
	return r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, session)), true
}
func groupCapabilityPath(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/builtin/") || strings.HasPrefix(p, "/api/tool/") || p == "/api/chat" || p == "/api/notify" {
		return r.Method == http.MethodPost
	}
	if strings.HasPrefix(p, "/api/user/secrets/") {
		return r.Method == http.MethodGet
	}
	return r.Method == http.MethodGet && (strings.HasPrefix(p, "/plugins/") || strings.HasPrefix(p, "/data/") || strings.HasPrefix(p, "/screenshots/") || p == "/widget-base.css" || p == "/prism-widget.js" || p == "/api/tools")
}
