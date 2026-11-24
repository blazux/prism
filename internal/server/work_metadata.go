package server

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

// One compact snapshot of the latest turn in a conversation. This is display
// metadata, not a new job lifecycle, generated progress report or reasoning log.
type workMetadata struct {
	Request        string         `json:"request,omitempty"`
	RequestID      string         `json:"requestId,omitempty"`
	RunStatus      string         `json:"runStatus"`
	StartedAt      *time.Time     `json:"startedAt,omitempty"`
	LastActivityAt *time.Time     `json:"lastActivityAt,omitempty"`
	FinishedAt     *time.Time     `json:"finishedAt,omitempty"`
	RecentTools    []activityTool `json:"recentTools"`
	RoomMessageID  int64          `json:"roomMessageId,omitempty"`
}

func runMetadataKey(c *Client) string {
	return "chat_work_" + strings.TrimPrefix(runStateKey(c), "chat_run_")
}
func timePointer(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	v := t.UTC()
	return &v
}
func (s *Server) saveWorkMetadata(ctx context.Context, c *Client, meta workMetadata) error {
	ms := s.store()
	if ms == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	meta.Request = activityText(meta.Request)
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return ms.SetConfigs(ctx, map[string]string{runStateKey(c): meta.RunStatus, runMetadataKey(c): string(raw)})
}
func metadataFromRun(r *chatRun, status string) workMetadata {
	return workMetadata{Request: r.request, RequestID: r.requestID, RunStatus: status, StartedAt: timePointer(r.startedAt), LastActivityAt: timePointer(r.lastActivityAt), FinishedAt: timePointer(r.finishedAt), RecentTools: append([]activityTool(nil), r.recentTools...)}
}

type agentJobContextKey struct{}

func workOwner(session string, actingUserID int64) *Client {
	var user *memory.User
	if scope := groupScopeFromSessionID(session); scope != "" {
		gid, _ := strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
		if gid > 0 {
			user = &memory.User{ID: -gid}
		}
	} else if scope := personalScopeFromSessionID(session); scope != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(scope, "u"), 10, 64)
		if id > 0 {
			user = &memory.User{ID: id}
		}
	} else if actingUserID > 0 {
		user = &memory.User{ID: actingUserID}
	}
	return &Client{user: user, sessionID: session}
}

// Headless turns already have sessionJob ownership/cancellation. Attach small
// metadata there; child workers and concurrent parents retain their own scope.
func (s *Server) beginHeadlessWork(ctx context.Context, session, message string, actingUserID int64) (func(agent.Event), func(error)) {
	noop := func(agent.Event) {}
	done := func(error) {}
	if options, _ := ctx.Value(childOptionsKey{}).(*childOptions); options != nil {
		return noop, done
	}
	if compact, _ := ctx.Value(compactContextKey{}).(bool); compact {
		return noop, done
	}
	job, _ := ctx.Value(agentJobContextKey{}).(*sessionJob)
	if job == nil {
		return noop, done
	}
	c := workOwner(session, actingUserID)
	s.runsMu.Lock()
	if s.runs[runKey(c)] != nil {
		s.runsMu.Unlock()
		return noop, done
	}
	for other := range s.sessionJobs[session] {
		if other != job && other.work != nil {
			s.runsMu.Unlock()
			return noop, done
		}
	}
	now := time.Now().UTC()
	meta := &workMetadata{Request: activityText(message), RunStatus: "running", StartedAt: &now, LastActivityAt: &now}
	if scope := groupScopeFromSessionID(session); strings.HasPrefix(session, "room-g") && scope != "" && s.rooms != nil {
		gid, _ := strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
		s.rooms.mu.Lock()
		if progress := s.rooms.progress[gid]; progress != nil {
			meta.Request = progress.request
			meta.RoomMessageID = progress.messageID
		}
		s.rooms.mu.Unlock()
	}
	job.work = meta
	if err := s.saveWorkMetadata(ctx, c, *meta); err != nil {
		log.Printf("[activity] headless start metadata unavailable: %v", err)
	}
	s.runsMu.Unlock()
	track := func(ev agent.Event) {
		s.runsMu.Lock()
		defer s.runsMu.Unlock()
		if job.work != meta {
			return
		}
		now := time.Now().UTC()
		meta.LastActivityAt = &now
		if ev.Type == "tool_use" {
			meta.RecentTools = append(meta.RecentTools, activityTool{ID: ev.ID, Tool: ev.Tool, Status: "running"})
			if len(meta.RecentTools) > 8 {
				meta.RecentTools = meta.RecentTools[1:]
			}
		}
		if ev.Type == "tool_result" {
			for i := range meta.RecentTools {
				if meta.RecentTools[i].ID == ev.ID {
					meta.RecentTools[i].Status = "completed"
					if ev.IsError {
						meta.RecentTools[i].Status = "failed"
					}
				}
			}
		}
	}
	finish := func(err error) {
		s.runsMu.Lock()
		defer s.runsMu.Unlock()
		if job.work != meta {
			return
		}
		status := "completed"
		if err != nil {
			status = "failed"
		}
		if ctx.Err() != nil {
			status = "stopped"
			if ctx.Err() == context.DeadlineExceeded {
				status = "timed_out"
			}
		}
		if s.runtimeContext().Err() != nil {
			status = "interrupted"
		}
		now := time.Now().UTC()
		meta.RunStatus = status
		meta.FinishedAt = &now
		meta.LastActivityAt = &now
		// A newer dashboard turn may have replaced this headless turn's metadata.
		// Never let the older completion overwrite that newer request.
		if s.runs[runKey(c)] != nil {
			return
		}
		save, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if ms := s.store(); ms != nil {
			raw, _, readErr := ms.GetConfig(save, runMetadataKey(c))
			var current workMetadata
			if readErr != nil || json.Unmarshal([]byte(raw), &current) != nil || current.StartedAt == nil || !current.StartedAt.Equal(*meta.StartedAt) {
				return
			}
		}
		if err := s.saveWorkMetadata(save, c, *meta); err != nil {
			log.Printf("[activity] headless finish metadata unavailable: %v", err)
		}
	}
	return track, finish
}

func snapshotHeadlessLocked(s *Server, session string) *workMetadata {
	var result *workMetadata
	for job := range s.sessionJobs[session] {
		if job.work != nil && (result == nil || result.StartedAt == nil || job.work.StartedAt != nil && job.work.StartedAt.After(*result.StartedAt)) {
			copy := *job.work
			copy.RecentTools = append([]activityTool(nil), job.work.RecentTools...)
			result = &copy
		}
	}
	return result
}
