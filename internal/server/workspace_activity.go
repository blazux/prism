package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"prism/internal/memory"
)

const workspaceActivityHelp = "To check work in another workspace, use exec_command to GET $PRISM_URL/api/sessions, then GET $PRISM_URL/api/sessions/<id>/activity with Authorization: Bearer $PRISM_TOKEN. This reads task status, recent messages, tools and subagents without starting a turn. Read prism_help(topic=workspace-activity) for details. Do not POST /api/chat merely to ask for progress: it starts another agent turn."

type workspaceActivity struct {
	Session        string             `json:"session"`
	ObservedAt     time.Time          `json:"observedAt"`
	RunStatus      string             `json:"runStatus"`
	Request        string             `json:"request,omitempty"`
	Messages       []activityMessage  `json:"messages"`
	RecentTools    []activityTool     `json:"recentTools"`
	Subagents      []activitySubagent `json:"subagents"`
	BackgroundJobs int                `json:"backgroundJobs"`
}

type activityMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

type activityTool struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Status string `json:"status"`
}

type activitySubagent struct {
	ID     string `json:"id"`
	Task   string `json:"task,omitempty"`
	Status string `json:"status"`
}

func activityText(text string) string {
	const max = 1000
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return string([]rune(text)[:max]) + "…"
}

// This GET deliberately reads another workspace of the SAME owner. Do not
// relax sessionFor: execution, secrets and mutations remain session-bound.
func (s *Server) handleWorkspaceActivity(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if len(id) > 128 || !canonicalSessionRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	u := currentUser(r)
	if s.cfg.MultiUser && (u == nil || u.ID < 0) {
		http.Error(w, "personal workspace access required", http.StatusForbidden)
		return
	}
	ms := s.store()
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	owner, exists, err := ms.SessionOwner(r.Context(), id)
	if err != nil {
		http.Error(w, "workspace unavailable", http.StatusInternalServerError)
		return
	}
	if !exists || (u != nil && u.ID > 0 && (owner == nil || *owner != u.ID || !strings.HasPrefix(id, "u"+strconv.FormatInt(u.ID, 10)+"-"))) {
		http.NotFound(w, r)
		return
	}
	limit := 4
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 20 {
			http.Error(w, "limit must be between 1 and 20", http.StatusBadRequest)
			return
		}
	}
	entries, err := ms.LoadActivityMessages(r.Context(), id, limit)
	if err != nil {
		http.Error(w, "conversation unavailable", http.StatusInternalServerError)
		return
	}
	activity := workspaceActivity{Session: id, ObservedAt: time.Now().UTC(), RunStatus: "idle", Messages: []activityMessage{}, RecentTools: []activityTool{}, Subagents: []activitySubagent{}}
	for _, entry := range entries {
		activity.Messages = append(activity.Messages, activityMessage{Role: entry.Role, Content: entry.Content, CreatedAt: entry.CreatedAt})
	}
	c := &Client{user: u, sessionID: id}
	if s.cfg.MultiUser && owner != nil && *owner > 0 {
		c.user = &memory.User{ID: *owner}
	}
	// Read the persisted state while holding runsMu too, so a task finishing
	// between lookups cannot be mistaken for a server restart.
	s.runsMu.Lock()
	activity.BackgroundJobs = len(s.sessionJobs[id])
	if run := s.runs[runKey(c)]; run != nil {
		run.mu.Lock()
		activity.RunStatus = run.status
		activity.Request = run.request
		activity.RecentTools = append(activity.RecentTools, run.recentTools...)
		for _, child := range run.children {
			activity.Subagents = append(activity.Subagents, activitySubagent{ID: child.id, Task: child.task, Status: child.status})
		}
		run.mu.Unlock()
	} else if activity.BackgroundJobs > 0 {
		activity.RunStatus = "running"
	} else {
		status, _, stateErr := ms.GetConfig(r.Context(), runStateKey(c))
		if stateErr != nil {
			s.runsMu.Unlock()
			http.Error(w, "task state unavailable", http.StatusInternalServerError)
			return
		}
		if status != "" {
			activity.RunStatus = status
			if status == "running" {
				activity.RunStatus = "interrupted"
			}
		}
	}
	s.runsMu.Unlock()
	sort.Slice(activity.Subagents, func(i, j int) bool { return activity.Subagents[i].ID < activity.Subagents[j].ID })
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(activity)
}
