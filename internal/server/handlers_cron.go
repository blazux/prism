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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cronJobMarker = "# agent-job: "
const cronDisabledPrefix = "#DISABLED# "

type CronJob struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
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
		// Find the following non-empty line: the job itself.
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j >= len(lines) {
			break
		}
		jobLine := strings.TrimRight(lines[j], "\r")
		enabled := true
		if strings.HasPrefix(jobLine, cronDisabledPrefix) {
			enabled = false
			jobLine = strings.TrimPrefix(jobLine, cronDisabledPrefix)
		}
		schedule, command := splitSchedule(jobLine)
		jobs = append(jobs, CronJob{Name: name, Schedule: schedule, Command: command, Enabled: enabled})
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
			// Locate the job line.
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) {
				cur := strings.TrimRight(lines[j], "\r")
				bare := strings.TrimPrefix(cur, cronDisabledPrefix)
				repl := fn(bare)
				if repl == "" {
					i = j // drop marker + job line
					continue
				}
				out = append(out, l, repl)
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

// GET /api/cron, POST {name,enabled}, DELETE ?name=
func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	switch r.Method {
	case "GET":
		jobs := parseCronJobs(s.readCrontab())
		if jobs == nil {
			jobs = []CronJob{}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"jobs": jobs})
	case "POST":
		var b struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Name == "" {
			http.Error(w, "bad body", 400)
			return
		}
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
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	case "DELETE":
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name required", 400)
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
