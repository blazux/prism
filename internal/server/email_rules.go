package server

import (
	"context"
	"encoding/json"
	"fmt"
	"prism/internal/agent"
	"prism/internal/email"
	"prism/internal/memory"
	"time"
)

// Deterministic inbox rules require no chat turn and work while the UI is closed.
// Use each account's executor so configuration scope and tool policy agree with
// the interactive email tool. No shared-agent or group mailbox is invented.
func (s *Server) startEmailRules() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.runEmailRules()
		}
	}()
}
func (s *Server) runEmailRules() {
	ms := s.store()
	if ms == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	users := []memory.User{*serviceUser}
	if s.cfg.MultiUser {
		var err error
		users, err = ms.ListUsers(ctx)
		if err != nil {
			return
		}
	}
	for _, u := range users {
		if ctx.Err() != nil {
			return
		}
		if u.ID > 0 && u.Status != memory.StatusApproved {
			continue
		}
		session, scope := "default", ""
		if u.ID > 0 {
			session = fmt.Sprintf("u%d-assistant", u.ID)
			scope = fmt.Sprintf("u%d", u.ID)
		}
		rules, err := email.LoadRules(ctx, ms.ConfigScope(scope))
		if err != nil {
			continue
		}
		enabled := false
		for _, r := range rules {
			enabled = enabled || r.Enabled
		}
		if !enabled {
			continue
		}
		ex := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, s.cfg.PluginDir, s.cfg.SearxngURL, s.selfCallToken(session))
		ex.SetRawResults(true)
		ex.SetSessionID(session)
		ex.SetMemoryStore(ms)
		s.callerContextForUser(ctx, &u, session).apply(ex)
		// A disabled email tool must not generate a denial every minute.
		args := json.RawMessage(`{"action":"rules_apply"}`)
		if err := ex.Authorize("email", args); err != nil {
			continue
		}
		_, _, err = ex.Execute(ctx, "email", args)
		if err != nil {
			ms.AddUsage(ctx, u.ID, session, "mail_rule", "Email rules", 1, map[string]any{"error": err.Error()})
			continue
		}
	}
}
