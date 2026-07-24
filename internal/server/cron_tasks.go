package server

// Cron jobs surfaced in the Tasks list, read-only — the same reasoning that
// already puts Vortex's queued outbound calls there (see vox.go): a recurring
// job ("check the RT queue every 15 minutes") is exactly the kind of pending
// thing a user expects to find when they look at what's scheduled, not a
// separate silo only the agent ever sees. The agent still owns the verbs
// (cron action=add/remove); this is display only.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"prism/internal/agent"
	"prism/internal/tasks"
)

// Cron jobs surfaced in Tasks carry a "cron:" id so they can never be mistaken
// for a row of the tasks table, mirroring vox.go's "call:" convention.
const cronTaskPrefix = "cron:"

const cronTaskReadOnlyMsg = "This is a scheduled cron job, not a to-do. Ask the agent to remove it (cron action=remove)."

func isCronTaskID(id string) bool { return strings.HasPrefix(id, cronTaskPrefix) }

// cronOwnerScope mirrors the agent's own cronOwner() (internal/agent/tools_cron.go)
// so a job's "# agent-owner:" tag and this lookup agree on the same value:
// "u<id>" for a real user, "default" for the service identity / legacy no-DB
// mode (where cron jobs are created under session "default").
func (s *Server) cronOwnerScope(r *http.Request) string {
	if u := currentUser(r); u != nil && u.ID > 0 {
		return fmt.Sprintf("u%d", u.ID)
	}
	return "default"
}

// cronPendingJobTasks reads the persisted crontab (kept in sync with the live
// one on every cron_add/cron_remove — see writeCrontab) directly off the
// shared workspace volume, rather than shelling into prism-workspace: never
// fatal, so a slow or unreachable workspace container must not break the
// user's real tasks from loading.
func (s *Server) cronPendingJobTasks(r *http.Request) []tasks.Item {
	raw, err := os.ReadFile(filepath.Join(s.cfg.WorkspaceDir, ".crontab"))
	if err != nil {
		return nil
	}
	scope := s.cronOwnerScope(r)
	var out []tasks.Item
	for _, j := range agent.ParseCronJobs(string(raw)) {
		if j.Owner != "" && j.Owner != scope {
			continue // another user's job
		}
		if !j.Enabled {
			continue // paused from the Tasks app — not a pending to-do
		}
		title := fmt.Sprintf("⏰ %s — %s", j.Name, j.Schedule)
		if j.Desc != "" {
			title += " (" + j.Desc + ")"
		}
		out = append(out, tasks.Item{
			ID:       cronTaskPrefix + j.Name,
			Title:    title,
			Done:     false,
			Priority: "normal",
		})
	}
	return out
}
