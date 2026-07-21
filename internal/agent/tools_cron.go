package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// shQuote single-quotes a value for safe use as a POSIX shell word, escaping
// any embedded single quotes the standard way ('\'').
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type CronJob struct{ Name, Owner, Desc, Schedule, Command string }

// ParseCronJobs splits a crontab into the agent-managed jobs (marker blocks).
// Exported so internal/server can render pending cron jobs into the Tasks
// list (read-only) without re-implementing this parser.
func ParseCronJobs(raw string) []CronJob {
	var jobs []CronJob
	var cur *CronJob
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
			cur = &CronJob{Name: strings.TrimSpace(strings.TrimPrefix(t, "# agent-job:"))}
		case cur != nil && strings.HasPrefix(t, "# agent-owner:"):
			cur.Owner = strings.TrimSpace(strings.TrimPrefix(t, "# agent-owner:"))
		case cur != nil && strings.HasPrefix(t, "# agent-desc:"):
			cur.Desc = strings.TrimSpace(strings.TrimPrefix(t, "# agent-desc:"))
		case t == "" || strings.HasPrefix(t, "#"):
			// blank line or unrelated comment — ignore
		default:
			if cur != nil && cur.Schedule == "" {
				fields := strings.Fields(t)
				if len(fields) >= 6 {
					cur.Schedule = strings.Join(fields[:5], " ")
					cur.Command = strings.Join(fields[5:], " ")
				} else {
					cur.Schedule = t
				}
				flush()
			}
		}
	}
	flush()
	return jobs
}

// cronEnvPrefixRe matches the "PRISM_URL=... PRISM_SESSION=... PRISM_TOKEN=... "
// prefix cronAdd injects ahead of every command (see cronAdd) — stripped again
// for display so cron_list doesn't dump the live token in every listing.
var cronEnvPrefixRe = regexp.MustCompile(`^PRISM_URL='[^']*' PRISM_SESSION='[^']*' PRISM_TOKEN='[^']*' `)

func displayCommand(command string) string {
	return cronEnvPrefixRe.ReplaceAllString(command, "")
}

func (e *ToolExecutor) cronList(ctx context.Context) (string, error) {
	raw, err := e.docker.Exec(ctx, "crontab -l 2>/dev/null || true", 10*time.Second)
	if err != nil {
		return "(no jobs scheduled)", nil
	}
	owner := e.cronOwner()
	var out []string
	for _, j := range ParseCronJobs(raw) {
		if j.Owner != "" && j.Owner != owner {
			continue // another user's job
		}
		line := fmt.Sprintf("• %s — %s  %s", j.Name, j.Schedule, displayCommand(j.Command))
		if j.Desc != "" {
			line += "  (" + j.Desc + ")"
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

	// Also export them as real env vars for the command's own process — every
	// other execution path (exec_command, register_tool) gives a script
	// os.environ['PRISM_TOKEN'] etc. directly; a cron job is not a special case
	// a model needs to remember differently, it should just work the same way.
	// The text substitution above is still needed separately: a shell expands
	// $VAR in a command's own arguments before this same line's prefix
	// assignments take effect, so it wouldn't see them otherwise.
	command = fmt.Sprintf("PRISM_URL=%s PRISM_SESSION=%s PRISM_TOKEN=%s %s",
		shQuote("http://prism-server:8080"), shQuote(session), shQuote(e.prismToken), command)

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
	var target *CronJob
	for _, j := range ParseCronJobs(current) {
		if j.Name == name {
			jj := j
			target = &jj
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("no job named %q found", name), nil
	}
	if target.Owner != "" && target.Owner != owner {
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
