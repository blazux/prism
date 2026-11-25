package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"prism/internal/memory"
)

type activityWorkItem struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ConversationURL string `json:"conversationUrl"`
	workMetadata
	Subagents      []activitySubagent `json:"subagents"`
	BackgroundJobs int                `json:"backgroundJobs"`
	Queued         int                `json:"queued"`
}

func knownWorkStatus(status string) bool {
	switch status {
	case "running", "queued", "waiting_for_input", "waiting_for_approval", "completed", "failed", "stopped", "interrupted", "timed_out":
		return true
	}
	return false
}
func savedWork(configs map[string]string, c *Client) workMetadata {
	var meta workMetadata
	_ = json.Unmarshal([]byte(configs[runMetadataKey(c)]), &meta)
	status := configs[runStateKey(c)]
	if status != "" && status != meta.RunStatus {
		// Legacy state without matching metadata must not inherit an older request.
		meta = workMetadata{RunStatus: status}
	}
	if meta.RunStatus == "running" || meta.RunStatus == "waiting_for_input" || meta.RunStatus == "waiting_for_approval" {
		meta.RunStatus = "interrupted"
		meta.FinishedAt = nil
	}
	meta.Request = activityText(meta.Request)
	return meta
}
func normalizeWork(item *activityWorkItem) {
	if item.RecentTools == nil {
		item.RecentTools = []activityTool{}
	}
	if item.Subagents == nil {
		item.Subagents = []activitySubagent{}
	}
	if item.RunStatus == "running" || item.RunStatus == "queued" || strings.HasPrefix(item.RunStatus, "waiting_") {
		item.FinishedAt = nil
	} else {
		// A retained tool start with no result is not evidence it is still
		// executing after the conversation stopped or the server restarted.
		for i := range item.RecentTools {
			if item.RecentTools[i].Status == "running" {
				item.RecentTools[i].Status = "result_unavailable"
			}
		}
	}
}

// A bounded metadata view of the latest work in each accessible conversation.
// Admin audit scope never grants another member's personal workspace access.
// One batch reads saved state; live overlays do not start agent turns.
func (s *Server) handleActivityWork(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET required.")
		return
	}
	u := currentUser(r)
	if s.cfg.MultiUser && (u == nil || u.ID <= 0) {
		writeErr(w, 403, "A personal account is required to view work.")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, 503, "Activity storage is unavailable.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	sessions, err := ms.ListSessions(ctx, ownerPtr(u))
	if err != nil {
		writeErr(w, 500, "Could not load your conversations.")
		return
	}
	groups := []memory.Group{}
	if s.cfg.MultiUser && u != nil {
		if u.IsGlobalAdmin() {
			groups, err = ms.ListGroups(ctx)
		} else {
			var memberships []memory.Membership
			memberships, err = ms.UserGroups(ctx, u.ID)
			for _, m := range memberships {
				groups = append(groups, memory.Group{ID: m.GroupID, Name: m.GroupName})
			}
		}
		if err != nil {
			writeErr(w, 500, "Could not load your Rooms.")
			return
		}
	}
	keys := make([]string, 0, 2*(len(sessions)+len(groups)))
	for _, session := range sessions {
		if u != nil && u.ID > 0 && !strings.HasPrefix(session.ID, "u"+strconv.FormatInt(u.ID, 10)+"-") {
			continue
		}
		c := &Client{user: u, sessionID: session.ID}
		keys = append(keys, runStateKey(c), runMetadataKey(c))
	}
	gids := make([]int64, 0, len(groups))
	for _, group := range groups {
		c := workOwner(fmt.Sprintf("room-g%d", group.ID), 0)
		keys = append(keys, runStateKey(c), runMetadataKey(c))
		gids = append(gids, group.ID)
	}
	// Keep persistence and live state in one snapshot so a turn that just ended
	// cannot be mistaken for a process interruption between two lookups.
	s.runsMu.Lock()
	runsLocked, roomsLocked := true, false
	unlock := func() {
		if roomsLocked {
			s.rooms.mu.Unlock()
			roomsLocked = false
		}
		if runsLocked {
			s.runsMu.Unlock()
			runsLocked = false
		}
	}
	defer unlock()
	configs, err := ms.GetConfigs(ctx, keys)
	if err != nil {
		unlock()
		writeErr(w, 500, "Could not load task states.")
		return
	}
	items := []activityWorkItem{}
	for _, session := range sessions {
		if u != nil && u.ID > 0 && !strings.HasPrefix(session.ID, "u"+strconv.FormatInt(u.ID, 10)+"-") {
			continue
		}
		c := &Client{user: u, sessionID: session.ID}
		item := activityWorkItem{ID: "workspace:" + session.ID, Name: session.Name, ConversationURL: "/?workspace=" + url.QueryEscape(session.ID) + "&chat=open", workMetadata: savedWork(configs, c), BackgroundJobs: s.agentJobsLocked(session.ID)}
		if run := s.runs[runKey(c)]; run != nil {
			run.mu.Lock()
			item.workMetadata = metadataFromRun(run, run.status)
			for _, child := range run.children {
				item.Subagents = append(item.Subagents, activitySubagent{ID: child.id, Task: activityText(child.task), Status: child.status})
			}
			run.mu.Unlock()
		} else if live := snapshotHeadlessLocked(s, session.ID); live != nil {
			item.workMetadata = *live
		} else if item.BackgroundJobs > 0 {
			item.workMetadata = workMetadata{RunStatus: "running"}
		}
		if !knownWorkStatus(item.RunStatus) {
			continue
		}
		normalizeWork(&item)
		items = append(items, item)
	}
	if s.rooms != nil {
		s.rooms.mu.Lock()
		roomsLocked = true
	}
	latestRooms, err := ms.LatestRoomWork(ctx, gids)
	if err != nil {
		unlock()
		writeErr(w, 500, "Could not load Room activity.")
		return
	}
	for _, group := range groups {
		session := fmt.Sprintf("room-g%d", group.ID)
		c := workOwner(session, 0)
		meta := savedWork(configs, c)
		item := activityWorkItem{ID: fmt.Sprintf("room:%d", group.ID), Name: "Room · " + group.Name, ConversationURL: "/?app=room&group=" + strconv.FormatInt(group.ID, 10), BackgroundJobs: s.agentJobsLocked(session), workMetadata: meta}
		if row, ok := latestRooms[group.ID]; ok {
			// A companion snapshot identifies its actual saved reply. For older
			// Rooms, only the saved reply's start and request are known.
			if meta.RoomMessageID != row.MessageID && (meta.StartedAt == nil || !meta.StartedAt.After(row.CreatedAt)) {
				status := map[string]string{"complete": "completed", "error": "failed", "stopped": "stopped", "running": "interrupted"}[row.Status]
				item.workMetadata = workMetadata{RunStatus: status, Request: activityText(row.Request), StartedAt: timePointer(row.CreatedAt)}
			}
		}
		active := false
		if s.rooms != nil {
			item.Queued = len(s.rooms.queues[group.ID])
			active = s.rooms.cancels[group.ID] != nil || s.rooms.executing[group.ID]
			if active {
				item.workMetadata = workMetadata{RunStatus: "running"}
				if progress := s.rooms.progress[group.ID]; progress != nil {
					item.Request = progress.request
					item.RecentTools = append([]activityTool(nil), progress.tools...)
				}
			}
		}
		if live := snapshotHeadlessLocked(s, session); live != nil {
			item.workMetadata = *live
			item.RunStatus = "running"
		} else if item.BackgroundJobs > 0 && !active {
			item.workMetadata = workMetadata{RunStatus: "running"}
		}
		if !active && item.BackgroundJobs == 0 && item.Queued > 0 {
			item.workMetadata = workMetadata{RunStatus: "queued"}
		}
		if !knownWorkStatus(item.RunStatus) {
			continue
		}
		normalizeWork(&item)
		items = append(items, item)
	}
	unlock()
	sort.Slice(items, func(i, j int) bool {
		stamp := func(item activityWorkItem) time.Time {
			if item.LastActivityAt != nil {
				return *item.LastActivityAt
			}
			if item.StartedAt != nil {
				return *item.StartedAt
			}
			return time.Time{}
		}
		a, b := stamp(items[i]), stamp(items[j])
		if a.Equal(b) {
			return items[i].ID < items[j].ID
		}
		return a.After(b)
	})
	writeJSON(w, map[string]any{"items": items, "observedAt": time.Now().UTC()})
}
