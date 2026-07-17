package server

// REST view of the agent's scheduled jobs (the workspace crontab). The Tasks app
// uses this to show jobs visually and let the user enable/disable or delete each
// one without going through the agent. Jobs are stored as marker/command pairs:
//
//	# agent-job: <name>
//	<schedule> <command>
//
// Disabling a job comments its command line with a "#DISABLED# " prefix so it is
// preserved (and can be re-enabled) but not run by cron.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cronJobMarker = "# agent-job: "
const cronOwnerMarker = "# agent-owner: "
const cronDescMarker = "# agent-desc: "
const cronDisabledPrefix = "#DISABLED# "

type CronJob struct {
	Name        string `json:"name"`
	Owner       string `json:"owner,omitempty"` // "u<id>" — who scheduled it
	Description string `json:"description"`
	Schedule    string `json:"schedule"`
	Command     string `json:"command"`
	Enabled     bool   `json:"enabled"`
}

// scanJobBlock reads the comment lines following a job marker (owner and
// description in any order, blanks allowed) and returns them plus the index of
// the job line, or -1 if the block is truncated.
func scanJobBlock(lines []string, start int) (owner, desc string, jobIdx int) {
	j := start
	for j < len(lines) {
		l := strings.TrimRight(lines[j], "\r")
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			j++
		case strings.HasPrefix(l, cronOwnerMarker):
			owner = strings.TrimSpace(strings.TrimPrefix(l, cronOwnerMarker))
			j++
		case strings.HasPrefix(l, cronDescMarker):
			desc = strings.TrimSpace(strings.TrimPrefix(l, cronDescMarker))
			j++
		default:
			return owner, desc, j
		}
	}
	return owner, desc, -1
}

func (s *Server) readCrontab() string {
	out, err := s.docker.Exec(context.Background(), "crontab -l 2>/dev/null || true", 10*time.Second)
	if err != nil {
		// Some Exec implementations dislike a nil ctx; retry below is unnecessary
		// since the handler passes context — kept defensive.
		return ""
	}
	return out
}

func (s *Server) applyCrontab(content string) error {
	path := filepath.Join(s.cfg.WorkspaceDir, ".crontab")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return err
	}
	_, err := s.docker.Exec(context.Background(), "crontab /workspace/.crontab", 10*time.Second)
	return err
}

// splitSchedule separates a cron schedule (5 fields, or a single @keyword) from
// the command that follows it.
func splitSchedule(line string) (schedule, command string) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "@") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			return parts[0], strings.TrimSpace(parts[1])
		}
		return parts[0], ""
	}
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return line, ""
	}
	return strings.Join(fields[:5], " "), strings.TrimSpace(strings.Join(fields[5:], " "))
}

func parseCronJobs(raw string) []CronJob {
	lines := strings.Split(raw, "\n")
	var jobs []CronJob
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(l, cronJobMarker) {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(l, cronJobMarker))
		owner, desc, j := scanJobBlock(lines, i+1)
		if j < 0 {
			break
		}
		jobLine := strings.TrimRight(lines[j], "\r")
		enabled := true
		if strings.HasPrefix(jobLine, cronDisabledPrefix) {
			enabled = false
			jobLine = strings.TrimPrefix(jobLine, cronDisabledPrefix)
		}
		schedule, command := splitSchedule(jobLine)
		jobs = append(jobs, CronJob{Name: name, Owner: owner, Description: desc, Schedule: schedule, Command: command, Enabled: enabled})
		i = j
	}
	return jobs
}

// mutateJob rewrites the crontab applying fn to the command line of the named
// job. fn receives the current (un-prefixed) line and returns the new line, or
// "" to drop the job (and its marker).
func (s *Server) mutateJob(name string, fn func(line string) string) error {
	raw := s.readCrontab()
	lines := strings.Split(raw, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if strings.HasPrefix(l, cronJobMarker) &&
			strings.TrimSpace(strings.TrimPrefix(l, cronJobMarker)) == name {
			// Preserve the block's comment lines (owner, description) verbatim.
			var meta []string
			j := i + 1
			for j < len(lines) {
				cl := strings.TrimRight(lines[j], "\r")
				t := strings.TrimSpace(cl)
				if t == "" || strings.HasPrefix(cl, cronOwnerMarker) || strings.HasPrefix(cl, cronDescMarker) {
					if t != "" {
						meta = append(meta, cl)
					}
					j++
					continue
				}
				break
			}
			if j < len(lines) {
				cur := strings.TrimRight(lines[j], "\r")
				bare := strings.TrimPrefix(cur, cronDisabledPrefix)
				repl := fn(bare)
				if repl == "" {
					i = j // drop marker + meta + job line
					continue
				}
				out = append(out, l)
				out = append(out, meta...)
				out = append(out, repl)
				i = j
				continue
			}
		}
		out = append(out, l)
	}
	content := strings.Join(out, "\n")
	content = strings.TrimRight(content, "\n") + "\n"
	if strings.TrimSpace(content) == "" {
		_, err := s.docker.Exec(context.Background(), "crontab -r 2>/dev/null || true", 10*time.Second)
		return err
	}
	return s.applyCrontab(content)
}

// editJobBlock rewrites an existing job's block (schedule + command + optional
// description) in place, re-enabling it. An empty desc keeps the current one.
func (s *Server) editJobBlock(name, schedule, command, desc string) error {
	raw := s.readCrontab()
	lines := strings.Split(raw, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if strings.HasPrefix(l, cronJobMarker) &&
			strings.TrimSpace(strings.TrimPrefix(l, cronJobMarker)) == name {
			oldOwner, oldDesc, j := scanJobBlock(lines, i+1)
			if j >= 0 {
				d := desc
				if d == "" {
					d = oldDesc
				}
				out = append(out, l)
				if oldOwner != "" {
					out = append(out, cronOwnerMarker+oldOwner)
				}
				if d != "" {
					out = append(out, cronDescMarker+d)
				}
				out = append(out, schedule+" "+command)
				i = j
				continue
			}
		}
		out = append(out, l)
	}
	content := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	return s.applyCrontab(content)
}

// upsertCronJob creates a job (stamped with owner) or, if a job with that name
// exists, replaces its schedule + command + description (re-enabling it, keeping
// the original owner).
func (s *Server) upsertCronJob(name, schedule, command, desc, owner string) error {
	name = strings.TrimSpace(name)
	schedule = strings.TrimSpace(schedule)
	command = strings.TrimSpace(command)
	desc = strings.TrimSpace(desc)
	if name == "" || schedule == "" || command == "" {
		return fmt.Errorf("name, schedule and command are required")
	}
	if strings.ContainsAny(name+schedule+command+desc+owner, "\n\r") {
		return fmt.Errorf("fields must not contain newlines")
	}
	raw := s.readCrontab()
	for _, j := range parseCronJobs(raw) {
		if j.Name == name {
			return s.editJobBlock(name, schedule, command, desc)
		}
	}
	content := strings.TrimRight(raw, "\n")
	if content != "" {
		content += "\n"
	}
	content += cronJobMarker + name + "\n"
	if owner != "" {
		content += cronOwnerMarker + owner + "\n"
	}
	if desc != "" {
		content += cronDescMarker + desc + "\n"
	}
	content += schedule + " " + command + "\n"
	return s.applyCrontab(content)
}

// GET /api/cron, POST {name,enabled}, DELETE ?name= — owner-scoped: each user
// sees and manages only their own jobs (owner tag "u<id>", same as the agent's
// cron tools). A global admin sees everything, including legacy unowned jobs.
func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	u := currentUser(r)
	admin := u == nil || u.IsGlobalAdmin() // nil = service identity / legacy no-auth
	mine := ""
	if u != nil && u.ID > 0 {
		mine = fmt.Sprintf("u%d", u.ID)
	}
	canTouch := func(owner string) bool { return admin || (mine != "" && owner == mine) }
	findJob := func(name string) (CronJob, bool) {
		for _, j := range parseCronJobs(s.readCrontab()) {
			if j.Name == name {
				return j, true
			}
		}
		return CronJob{}, false
	}

	switch r.Method {
	case "GET":
		all := parseCronJobs(s.readCrontab())
		jobs := []CronJob{}
		for _, j := range all {
			if admin || (mine != "" && j.Owner == mine) {
				jobs = append(jobs, j)
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"jobs": jobs})
	case "POST":
		var b struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Schedule    string `json:"schedule"`
			Command     string `json:"command"`
			Enabled     bool   `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Name == "" {
			http.Error(w, "bad body", 400)
			return
		}
		if existing, ok := findJob(b.Name); ok && !canTouch(existing.Owner) {
			http.Error(w, "this job belongs to another user", 403)
			return
		}
		// schedule present → create/edit a job; otherwise → enable/disable toggle.
		if b.Schedule != "" {
			if err := s.upsertCronJob(b.Name, b.Schedule, b.Command, b.Description, mine); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		} else {
			err := s.mutateJob(b.Name, func(line string) string {
				if b.Enabled {
					return line // already un-prefixed by mutateJob
				}
				return cronDisabledPrefix + line
			})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	case "DELETE":
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name required", 400)
			return
		}
		if existing, ok := findJob(name); ok && !canTouch(existing.Owner) {
			http.Error(w, "this job belongs to another user", 403)
			return
		}
		if err := s.mutateJob(name, func(string) string { return "" }); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
