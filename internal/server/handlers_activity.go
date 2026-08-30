package server

import (
	"net/http"
	"strconv"

	"prism/internal/memory"
)

// handleActivity serves the human-facing activity feed: a chronological view of
// what the agent (and its automations) did — tool calls, tool errors, denials,
// chat turns, webhook fires — read from the usage_events audit table. It adds
// no capability for the agent; it just makes visible what the autonomous side
// of Prism has been doing (the ipam zombie, wasted verifications, which cron
// fired — as data, not by chance).
//
// Scope: single-user / the service identity see everything. A multi-user member
// sees their own sessions by default; an admin sees their own, or the whole
// deployment with ?scope=all.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	f := memory.ActivityFilter{Limit: limit}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)

	switch q.Get("view") {
	case "errors":
		f.Kinds = []string{"audit"}
		f.Items = []string{"tool_error", "tool_denied"}
	case "tools":
		f.Kinds = []string{"tool_call"}
	case "chat":
		f.Kinds = []string{"chat_turn"}
	case "webhooks":
		f.Kinds = []string{"webhook"}
	}
	if k := q.Get("session"); k != "" {
		if sid, ok := s.sessionFor(r, k); ok {
			f.Session = sid
		}
	}

	// Visibility scope.
	if caller := currentUser(r); caller != nil && caller.ID > 0 {
		if !(s.isAdminUser(r.Context(), caller) && q.Get("scope") == "all") {
			f.UserID = caller.ID
		}
	}

	events, err := ms.ActivityFeed(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var next int64
	if len(events) == limit && len(events) > 0 {
		next = events[len(events)-1].ID
	}
	writeJSON(w, map[string]interface{}{"events": events, "nextBefore": next})
}
