package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *ToolExecutor) cronList(ctx context.Context) (string, error) {
	out, err := e.docker.Exec(ctx, "crontab -l 2>/dev/null || echo '(no jobs scheduled)'", 10*time.Second)
	if err != nil {
		return "(no jobs scheduled)", nil
	}
	return strings.TrimSpace(out), nil
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
	entry := marker
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

	marker := "# agent-job: " + name
	lines := strings.Split(current, "\n")
	var kept []string
	skip := false
	removed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == marker {
			skip = true
			removed = true
			continue
		}
		if skip {
			// Drop the marker's block: an optional "# agent-desc:" line, then the
			// cron line. Keep skipping until we've consumed the cron command line.
			if strings.HasPrefix(strings.TrimSpace(line), "# agent-desc:") {
				continue
			}
			skip = false
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return fmt.Sprintf("no job named %q found", name), nil
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
