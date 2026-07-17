package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cronOwner is the tag under which the current session's cron jobs are recorded,
// so each user only sees and manages their own scheduled tasks in the shared
// crontab. Multi-user sessions are "u<id>-<workspace>" → owned by the user
// ("u<id>"); other sessions (shared room / webex agents) own by session id.
func (e *ToolExecutor) cronOwner() string {
	s := e.sessionID
	if s == "" {
		return "default"
	}
	if strings.HasPrefix(s, "u") {
		if i := strings.IndexByte(s, '-'); i > 1 {
			if _, err := strconv.Atoi(s[1:i]); err == nil {
				return s[:i] // "u<id>"
			}
		}
	}
	return s
}

type cronJob struct{ name, owner, desc, schedule, command string }

// parseCronJobs splits a crontab into the agent-managed jobs (marker blocks).
func parseCronJobs(raw string) []cronJob {
	var jobs []cronJob
	var cur *cronJob
	flush := func() {
		if cur != nil {
			jobs = append(jobs, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "# agent-job:"):
			flush()
			cur = &cronJob{name: strings.TrimSpace(strings.TrimPrefix(t, "# agent-job:"))}
		case cur != nil && strings.HasPrefix(t, "# agent-owner:"):
			cur.owner = strings.TrimSpace(strings.TrimPrefix(t, "# agent-owner:"))
		case cur != nil && strings.HasPrefix(t, "# agent-desc:"):
			cur.desc = strings.TrimSpace(strings.TrimPrefix(t, "# agent-desc:"))
		case t == "" || strings.HasPrefix(t, "#"):
			// blank line or unrelated comment — ignore
		default:
			if cur != nil && cur.schedule == "" {
				fields := strings.Fields(t)
				if len(fields) >= 6 {
					cur.schedule = strings.Join(fields[:5], " ")
					cur.command = strings.Join(fields[5:], " ")
				} else {
					cur.schedule = t
				}
				flush()
			}
		}
	}
	flush()
	return jobs
}

func (e *ToolExecutor) cronList(ctx context.Context) (string, error) {
	raw, err := e.docker.Exec(ctx, "crontab -l 2>/dev/null || true", 10*time.Second)
	if err != nil {
		return "(no jobs scheduled)", nil
	}
	owner := e.cronOwner()
	var out []string
	for _, j := range parseCronJobs(raw) {
		if j.owner != "" && j.owner != owner {
			continue // another user's job
		}
		line := fmt.Sprintf("• %s — %s  %s", j.name, j.schedule, j.command)
		if j.desc != "" {
			line += "  (" + j.desc + ")"
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return "(no jobs scheduled)", nil
	}
	return strings.Join(out, "\n"), nil
}

func (e *ToolExecutor) cronAdd(ctx context.Context, name, schedule, command, description string) (string, error) {
	if name == "" || schedule == "" || command == "" {
		return "", fmt.Errorf("name, schedule, and command are required")
	}
	// Reject newlines in any field to prevent crontab injection
	if strings.ContainsAny(name, "\n\r") {
		return "", fmt.Errorf("name must not contain newlines")
	}
	if strings.ContainsAny(schedule, "\n\r") {
		return "", fmt.Errorf("schedule must not contain newlines")
	}
	if strings.ContainsAny(command, "\n\r") {
		return "", fmt.Errorf("command must not contain newlines")
	}
	description = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(description, "\n", " "), "\r", " "))

	current, _ := e.docker.Exec(ctx, "crontab -l 2>/dev/null || true", 10*time.Second)
	current = strings.TrimSpace(current)

	marker := "# agent-job: " + name
	if strings.Contains(current, marker) {
		return "", fmt.Errorf("a job named %q already exists; use cron_remove first", name)
	}

	session := e.sessionID
	if session == "" {
		session = "default"
	}
	// Resolve $PRISM_URL and $PRISM_SESSION in the command now so cron doesn't
	// expand them in an empty environment (shell expands $VAR before inline
	// VAR=value assignments take effect).
	command = strings.ReplaceAll(command, "$PRISM_URL", "http://prism-server:8080")
	command = strings.ReplaceAll(command, "${PRISM_URL}", "http://prism-server:8080")
	command = strings.ReplaceAll(command, "$PRISM_SESSION", session)
	command = strings.ReplaceAll(command, "${PRISM_SESSION}", session)
	command = strings.ReplaceAll(command, "$PRISM_TOKEN", e.prismToken)
	command = strings.ReplaceAll(command, "${PRISM_TOKEN}", e.prismToken)

	// Tag the job with its owner so each user only manages their own tasks.
	entry := marker + "\n# agent-owner: " + e.cronOwner()
	if description != "" {
		entry += "\n# agent-desc: " + description
	}
	entry += fmt.Sprintf("\n%s %s", schedule, command)
	var newCrontab string
	if current == "" {
		newCrontab = entry + "\n"
	} else {
		newCrontab = current + "\n" + entry + "\n"
	}

	if err := e.writeCrontab(ctx, newCrontab); err != nil {
		return fmt.Sprintf("cron add failed: %v", err), nil
	}
	return fmt.Sprintf("Scheduled %q: %s %s", name, schedule, command), nil
}

func (e *ToolExecutor) cronRemove(ctx context.Context, name string) (string, error) {
	current, err := e.docker.Exec(ctx, "crontab -l 2>/dev/null || true", 10*time.Second)
	if err != nil || strings.TrimSpace(current) == "" {
		return "no cron jobs to remove", nil
	}

	// Authorize: the job must exist and belong to this user (legacy owner-less
	// jobs stay removable by anyone).
	owner := e.cronOwner()
	var target *cronJob
	for _, j := range parseCronJobs(current) {
		if j.name == name {
			jj := j
			target = &jj
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("no job named %q found", name), nil
	}
	if target.owner != "" && target.owner != owner {
		return fmt.Sprintf("job %q belongs to another user; you can only remove your own scheduled tasks", name), nil
	}

	marker := "# agent-job: " + name
	lines := strings.Split(current, "\n")
	var kept []string
	skip := false
	for _, line := range lines {
		if strings.TrimSpace(line) == marker {
			skip = true
			continue
		}
		if skip {
			// Drop the marker's block: optional "# agent-owner:" / "# agent-desc:"
			// lines, then the cron command line.
			tl := strings.TrimSpace(line)
			if strings.HasPrefix(tl, "# agent-owner:") || strings.HasPrefix(tl, "# agent-desc:") {
				continue
			}
			skip = false
			continue
		}
		kept = append(kept, line)
	}

	newCrontab := strings.TrimSpace(strings.Join(kept, "\n"))
	if newCrontab == "" {
		if _, err := e.docker.Exec(ctx, "crontab -r 2>/dev/null || true", 10*time.Second); err != nil {
			return fmt.Sprintf("crontab -r failed: %v", err), nil
		}
	} else {
		if err := e.writeCrontab(ctx, newCrontab+"\n"); err != nil {
			return fmt.Sprintf("cron_remove failed: %v", err), nil
		}
	}
	return fmt.Sprintf("Removed job %q", name), nil
}

// writeCrontab writes content to a temp file on the shared volume and applies it via `crontab`.
func (e *ToolExecutor) writeCrontab(ctx context.Context, content string) error {
	// Write to persistent path on the shared volume so cron jobs survive container recreation.
	persistPath := filepath.Join(e.workspaceDir, ".crontab")
	if err := os.WriteFile(persistPath, []byte(content), 0600); err != nil {
		return err
	}
	_, err := e.docker.Exec(ctx, "crontab /workspace/.crontab", 10*time.Second)
	return err
}

// ─── Custom tools ─────────────────────────────────────────────────────────────
