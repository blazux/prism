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

	"prism/internal/cronclock"
	"prism/internal/docker"
	"prism/internal/timeprefs"
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
// any embedded single quotes the standard way ('\”).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type CronJob struct {
	Name, Owner, Desc, Schedule, Command string
	Timezone                             string
	Enabled                              bool
}

// ownsCronJob says whether this session may edit or remove a job. Multi-user:
// only the owner (u<id>, or the shared-agent session). Single-user: every
// workspace belongs to the same person, so any job is theirs — the Tasks app
// and /api/cron already let them pause or delete any job, and the tool used to
// refuse the same request as "belongs to another user", which was false.
func (e *ToolExecutor) ownsCronJob(j CronJob) bool {
	return !e.multiUser || j.Owner == "" || j.Owner == e.cronOwner()
}

// cronDisabledPrefix marks a paused job's command line so it's preserved
// (and can be re-enabled) but not run by cron. Mirrors
// server/handlers_cron.go's cronDisabledPrefix constant — can't share it
// directly (internal/agent can't import internal/server), so it's
// duplicated as a literal; keep both in sync if it ever changes.
const cronDisabledPrefix = "#DISABLED# "

// splitCronSchedule separates a cron schedule (5 fields) from the command
// that follows it. Mirrors server/handlers_cron.go's splitSchedule for the
// same reason cronDisabledPrefix is duplicated above.
func splitCronSchedule(line string) (schedule, command string) {
	if sc, cmd, _, ok := cronclock.Unwrap(line); ok {
		return sc, cmd
	}
	if strings.HasPrefix(line, "@") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return line, ""
	}
	return strings.Join(fields[:5], " "), strings.Join(fields[5:], " ")
}

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
			cur = &CronJob{Name: strings.TrimSpace(strings.TrimPrefix(t, "# agent-job:")), Enabled: true}
		case cur != nil && strings.HasPrefix(t, "# agent-owner:"):
			cur.Owner = strings.TrimSpace(strings.TrimPrefix(t, "# agent-owner:"))
		case cur != nil && strings.HasPrefix(t, "# agent-desc:"):
			cur.Desc = strings.TrimSpace(strings.TrimPrefix(t, "# agent-desc:"))
		case cur != nil && cur.Schedule == "" && strings.HasPrefix(t, cronDisabledPrefix):
			// A paused job's line is itself a "#"-prefixed comment — must be
			// checked before the generic "unrelated comment, ignore" case
			// below, or a disabled job's Schedule/Command never get set (the
			// bug this case fixes: it showed up in the Tasks list with a
			// blank schedule instead of being recognized as paused).
			cur.Enabled = false
			_, _, cur.Timezone, _ = cronclock.Unwrap(strings.TrimPrefix(t, cronDisabledPrefix))
			cur.Schedule, cur.Command = splitCronSchedule(strings.TrimPrefix(t, cronDisabledPrefix))
			flush()
		case t == "" || strings.HasPrefix(t, "#"):
			// blank line or unrelated comment — ignore
		default:
			if cur != nil && cur.Schedule == "" {
				_, _, cur.Timezone, _ = cronclock.Unwrap(t)
				cur.Schedule, cur.Command = splitCronSchedule(t)
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
	raw, err := e.docker.Exec(ctx, docker.ReadCrontabCommand, 10*time.Second)
	if err != nil {
		return "", docker.CronReadError(err, raw)
	}
	owner := e.cronOwner()
	var out []string
	for _, j := range ParseCronJobs(raw) {
		if !e.ownsCronJob(j) {
			continue // another user's job
		}
		line := fmt.Sprintf("• %s — %s  %s", j.Name, j.Schedule, displayCommand(j.Command))
		if j.Timezone != "" {
			line += " [" + j.Timezone + "]"
		}
		if j.Desc != "" {
			line += "  (" + j.Desc + ")"
		}
		if j.Owner != "" && j.Owner != owner {
			line += "  [workspace: " + j.Owner + "]" // single-user: another board's job, still yours
		}
		if !j.Enabled {
			// A paused job looks identical otherwise — and the agent would
			// "fix" a widget that is only waiting for cron action=enable.
			line += "  [PAUSED — not running; cron action=enable resumes it]"
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
	if err := validateCronSchedule(schedule); err != nil {
		return "", err
	}
	description = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(description, "\n", " "), "\r", " "))

	current, err := e.docker.Exec(ctx, docker.ReadCrontabCommand, 10*time.Second)
	if err != nil {
		return "", docker.CronReadError(err, current)
	}
	current = strings.TrimSpace(current)

	marker := "# agent-job: " + name
	if strings.Contains(current, marker) {
		return "", fmt.Errorf("a job named %q already exists; remove it first with cron action=remove", name)
	}

	zone, err := e.prepareCronTimezone(ctx, schedule)
	if err != nil {
		return "", err
	}
	command = e.cronCommandLine(command)

	// Tag the job with its owner so each user only manages their own tasks.
	entry := marker + "\n# agent-owner: " + e.cronOwner()
	if description != "" {
		entry += "\n# agent-desc: " + description
	}
	entry += "\n" + cronclock.Wrap(schedule, command, zone)
	var newCrontab string
	if current == "" {
		newCrontab = entry + "\n"
	} else {
		newCrontab = current + "\n" + entry + "\n"
	}

	if err := e.writeCrontab(ctx, newCrontab); err != nil {
		return fmt.Sprintf("cron add failed: %v", err), nil
	}
	// displayCommand: the injected PRISM_TOKEN must not land in the model's
	// context (cron_list strips it the same way).
	return fmt.Sprintf("Scheduled %q: %s %s", name, schedule, displayCommand(command)), nil
}

// cronCommandLine turns the model's command into the line cron runs: PRISM_*
// resolved in the text and exported as env vars. Shared by add and update so an
// edited command gets exactly the environment a new one does.
func (e *ToolExecutor) cronCommandLine(command string) string {
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
	return fmt.Sprintf("PRISM_URL=%s PRISM_SESSION=%s PRISM_TOKEN=%s %s",
		shQuote("http://prism-server:8080"), shQuote(session), shQuote(e.prismToken), command)
}

// cronEditBlock rewrites one agent-managed job block in place, preserving its
// position, owner line and everything else in the crontab. edit receives the
// current description and the bare job line (schedule + command, disabled
// prefix stripped) and returns their replacements plus whether the job is
// enabled. Owner check as in cronRemove: a job that belongs to another user is
// never touched. Mirrors server/handlers_cron.go's mutateJob.
func (e *ToolExecutor) cronEditBlock(ctx context.Context, name string, edit func(desc, line string, enabled bool) (string, string, bool)) (*CronJob, string, error) {
	current, err := e.docker.Exec(ctx, docker.ReadCrontabCommand, 10*time.Second)
	if err != nil {
		// A failed exec is NOT an empty crontab: reporting "no cron jobs yet"
		// here made the model conclude the job did not exist.
		return nil, "", docker.CronReadError(err, current)
	}
	if strings.TrimSpace(current) == "" {
		return nil, "no cron jobs yet", nil
	}
	target, msg, content, err := e.rewriteCronBlock(current, name, edit)
	if err != nil || msg != "" {
		return nil, msg, err
	}
	if content == normalizeCrontab(current) {
		return target, "", nil // nothing changed (e.g. pausing an already-paused job): don't rewrite
	}
	if err := e.writeCrontab(ctx, content); err != nil {
		return nil, fmt.Sprintf("cron update failed: %v", err), nil
	}
	return target, "", nil
}

// normalizeCrontab puts raw crontab text in the exact shape rewriteCronBlock
// produces (single trailing newline), so an unchanged rewrite compares equal.
func normalizeCrontab(raw string) string {
	return strings.TrimRight(raw, "\n") + "\n"
}

// rewriteCronBlock is the pure part of cronEditBlock: given the current crontab
// text it returns the target job, a user-facing refusal message (job missing or
// not owned), and the full new crontab content. No I/O, so it is unit-testable.
func (e *ToolExecutor) rewriteCronBlock(current, name string, edit func(desc, line string, enabled bool) (string, string, bool)) (*CronJob, string, string, error) {
	jobs := ParseCronJobs(current)
	var target *CronJob
	for _, j := range jobs {
		if j.Name == name {
			jj := j
			target = &jj
			break
		}
	}
	if target == nil {
		names := make([]string, len(jobs))
		for i, j := range jobs {
			names[i] = j.Name
		}
		return nil, fmt.Sprintf("no job named %q found. Existing jobs: %s — use one of these names verbatim", name, strings.Join(names, ", ")), "", nil
	}
	if !e.ownsCronJob(*target) {
		return nil, fmt.Sprintf("job %q belongs to another user; you can only change your own scheduled tasks", name), "", nil
	}

	marker := "# agent-job: " + name
	lines := strings.Split(current, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(l) != marker {
			out = append(out, l)
			continue
		}
		out = append(out, l)
		// The block: optional owner/desc lines, then the job line.
		var ownerLine string
		desc := ""
		j := i + 1
		for j < len(lines) {
			t := strings.TrimSpace(strings.TrimRight(lines[j], "\r"))
			if strings.HasPrefix(t, "# agent-owner:") {
				ownerLine = t
				j++
				continue
			}
			if strings.HasPrefix(t, "# agent-desc:") {
				desc = strings.TrimSpace(strings.TrimPrefix(t, "# agent-desc:"))
				j++
				continue
			}
			break
		}
		if j >= len(lines) {
			return nil, "", "", fmt.Errorf("job %q has no command line in the crontab", name)
		}
		bare := strings.TrimPrefix(strings.TrimSpace(strings.TrimRight(lines[j], "\r")), cronDisabledPrefix)
		newDesc, newLine, enabled := edit(desc, bare, target.Enabled)
		if ownerLine != "" {
			out = append(out, ownerLine)
		}
		if newDesc != "" {
			out = append(out, "# agent-desc: "+newDesc)
		}
		if !enabled {
			newLine = cronDisabledPrefix + newLine
		}
		out = append(out, newLine)
		i = j
	}
	return target, "", normalizeCrontab(strings.Join(out, "\n")), nil
}

// cronSetEnabled pauses (kept, not run) or resumes a job — the toggle on the
// job's card in the Tasks app.
func (e *ToolExecutor) cronSetEnabled(ctx context.Context, name string, enabled bool) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required (from cron action=list)")
	}
	already := false
	job, msg, err := e.cronEditBlock(ctx, name, func(desc, line string, was bool) (string, string, bool) {
		already = was == enabled
		return desc, line, enabled
	})
	if err != nil || msg != "" {
		return msg, err
	}
	state := map[bool]string{true: "enabled", false: "paused"}[enabled]
	if already {
		return fmt.Sprintf("Job %q is already %s — nothing changed.", job.Name, state), nil
	}
	if enabled {
		return fmt.Sprintf("Job %q resumed: %s %s", job.Name, job.Schedule, displayCommand(job.Command)), nil
	}
	return fmt.Sprintf("Job %q paused — kept in the list, not run until cron action=enable.", job.Name), nil
}

// cronUpdate changes an existing job's schedule, command and/or description in
// place (same name, same position, enabled state kept) — instead of the
// remove-and-recreate dance, which lost the description and the paused state.
func (e *ToolExecutor) cronUpdate(ctx context.Context, name, schedule, command, description string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required (from cron action=list)")
	}
	if schedule == "" && command == "" && description == "" {
		return "", fmt.Errorf("update needs at least one of schedule, command, description")
	}
	for k, v := range map[string]string{"schedule": schedule, "command": command} {
		if strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("%s must not contain newlines", k)
		}
	}
	if schedule != "" {
		if err := validateCronSchedule(schedule); err != nil {
			return "", err
		}
	}
	description = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(description, "\n", " "), "\r", " "))
	zone := ""
	if schedule != "" {
		var err error
		zone, err = e.prepareCronTimezone(ctx, schedule)
		if err != nil {
			return "", err
		}
	}
	var newSchedule, newCommand string
	job, msg, err := e.cronEditBlock(ctx, name, func(desc, line string, enabled bool) (string, string, bool) {
		curSchedule, curCommand := splitCronSchedule(line)
		_, _, oldZone, _ := cronclock.Unwrap(line)
		if schedule == "" {
			zone = oldZone
		}
		newSchedule, newCommand = curSchedule, curCommand
		if schedule != "" {
			newSchedule = strings.TrimSpace(schedule)
		}
		if command != "" {
			newCommand = e.cronCommandLine(command)
		}
		if description != "" {
			desc = description
		}
		return desc, cronclock.Wrap(newSchedule, newCommand, zone), enabled
	})
	if err != nil || msg != "" {
		return msg, err
	}
	state := ""
	if !job.Enabled {
		state = " (still paused)"
	}
	return fmt.Sprintf("Updated job %q%s: %s %s", job.Name, state, newSchedule, displayCommand(newCommand)), nil
}

func (e *ToolExecutor) cronRemove(ctx context.Context, name string) (string, error) {
	current, err := e.docker.Exec(ctx, docker.ReadCrontabCommand, 10*time.Second)
	if err != nil {
		return "", docker.CronReadError(err, current)
	}
	if strings.TrimSpace(current) == "" {
		return "no cron jobs to remove", nil
	}

	// Authorize: the job must exist and belong to this user (legacy owner-less
	// jobs stay removable by anyone).
	jobs := ParseCronJobs(current)
	var target *CronJob
	for _, j := range jobs {
		if j.Name == name {
			jj := j
			target = &jj
			break
		}
	}
	if target == nil {
		names := make([]string, len(jobs))
		for i, j := range jobs {
			names[i] = j.Name
		}
		return fmt.Sprintf("no job named %q found. Existing jobs: %s — use one of these names verbatim", name, strings.Join(names, ", ")), nil
	}
	if !e.ownsCronJob(*target) {
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
		// Keep the persisted mirror in sync with the now-empty live crontab —
		// Dockerfile.agent's CMD reinstalls it (`crontab /workspace/.crontab`)
		// on every container restart. Leaving stale content here after
		// removing the last job would resurrect it on the next restart.
		persistPath := filepath.Join(e.workspaceDir, ".crontab")
		if err := e.writeManagedFile(persistPath, nil); err != nil {
			return fmt.Sprintf("cron_remove failed: %v", err), nil
		}
	} else {
		if err := e.writeCrontab(ctx, newCrontab+"\n"); err != nil {
			return fmt.Sprintf("cron_remove failed: %v", err), nil
		}
	}
	return fmt.Sprintf("Removed job %q", name), nil
}

var cronFieldRe = regexp.MustCompile(`^[0-9A-Za-z*,/\-]+$`)

// validateCronSchedule rejects what crontab would refuse — BEFORE anything is
// persisted. "every 5 min" is the classic model mistake; it must come back as a
// clear error, not as a poisoned mirror file.
func validateCronSchedule(s string) error {
	s = strings.TrimSpace(s)
	switch s {
	case "@reboot", "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly":
		return nil
	}
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return fmt.Errorf("schedule %q is not a cron expression: expected 5 fields (minute hour day-of-month month day-of-week), e.g. '*/5 * * * *' or '0 9 * * 1-5', or one of @hourly/@daily/@weekly/@monthly", s)
	}
	for i, f := range fields {
		if !cronFieldRe.MatchString(f) {
			return fmt.Errorf("schedule %q: field %d (%q) contains characters cron does not accept", s, i+1, f)
		}
	}
	return nil
}

// writeCrontab applies content via `crontab` and only THEN replaces the
// persistent mirror (/workspace/.crontab, re-applied on container restart): a
// crontab that the daemon rejects must never reach the mirror, or every job is
// lost at the next restart.
func (e *ToolExecutor) writeCrontab(ctx context.Context, content string) error {
	persistPath := filepath.Join(e.workspaceDir, ".crontab")
	tmpPath := persistPath + ".tmp"
	if err := e.writeManagedFile(tmpPath, []byte(content)); err != nil {
		return err
	}
	if _, err := e.docker.Exec(ctx, "crontab /workspace/.crontab.tmp", 10*time.Second); err != nil {
		e.removeManaged(tmpPath)
		return err
	}
	return os.Rename(tmpPath, persistPath)
}

// ─── Custom tools ─────────────────────────────────────────────────────────────

func (e *ToolExecutor) prepareCronTimezone(ctx context.Context, schedule string) (string, error) {
	store := e.userStore()
	if store == nil || schedule == "@reboot" {
		return "", nil
	}
	zone, _ := timeprefs.Read(ctx, store)
	if zone == "" {
		return "", nil
	}
	if err := e.writeManagedFile(filepath.Join(e.workspaceDir, cronclock.Path), cronclock.Script); err != nil {
		return "", err
	}
	if _, err := e.docker.Exec(ctx, "python3 /workspace/"+cronclock.Path+" "+shQuote(schedule)+" "+shQuote(zone)+" --check", 10*time.Second); err != nil {
		return "", fmt.Errorf("cannot schedule in %s: workspace needs Python zoneinfo/tzdata and a valid schedule: %w", zone, err)
	}
	return zone, nil
}
