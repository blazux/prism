package server

// REST config for the CalDAV connection shared by the calendar and tasks apps.
// Mirrors the email config pattern: JSON in agent_config, password in secrets.

import (
	"encoding/json"
	"net/http"
	"strings"

	"prism/internal/caldav"
)

func (s *Server) handleCalDAVConfig(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	switch r.Method {
	case "GET":
		cfg, ok := caldav.Load(r.Context(), s.memStore)
		writeJSON(w, map[string]interface{}{
			"configured": ok, "url": cfg.URL, "user": cfg.User,
			"eventPath": cfg.EventPath, "taskPath": cfg.TaskPath,
		})
	case "POST":
		var b struct {
			URL        string `json:"url"`
			User       string `json:"user"`
			Password   string `json:"password"`
			EventPath  string `json:"eventPath"`
			TaskPath   string `json:"taskPath"`
			Disconnect bool   `json:"disconnect"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Disconnect {
			s.memStore.SetConfig(r.Context(), caldav.KeyConfig, "")
			s.memStore.SetSecret(r.Context(), caldav.PasswordSecret, "")
			writeJSON(w, map[string]interface{}{"ok": true, "configured": false})
			return
		}
		cfg := caldav.Config{
			URL: strings.TrimSpace(b.URL), User: strings.TrimSpace(b.User),
			EventPath: strings.TrimSpace(b.EventPath), TaskPath: strings.TrimSpace(b.TaskPath),
			Pass: b.Password,
		}
		if cfg.URL == "" || cfg.User == "" {
			http.Error(w, "url and user required", 400)
			return
		}
		// Keep the existing password when the field is left blank (e.g. when only
		// re-pinning calendars).
		if cfg.Pass == "" {
			if old, ok := caldav.Load(r.Context(), s.memStore); ok {
				cfg.Pass = old.Pass
			}
		}
		if cfg.Pass == "" {
			http.Error(w, "password required", 400)
			return
		}
		// Validate by discovering the user's calendars.
		cals, err := cfg.Discover(r.Context())
		if err != nil {
			http.Error(w, "connection failed: "+err.Error(), 400)
			return
		}
		// Pin the chosen collections so later reads skip re-discovery.
		if cfg.EventPath == "" || cfg.TaskPath == "" {
			ev, task := caldav.PickPaths(cals)
			if cfg.EventPath == "" {
				cfg.EventPath = ev
			}
			if cfg.TaskPath == "" {
				cfg.TaskPath = task
			}
		}
		stored := cfg
		stored.Pass = ""
		raw, _ := json.Marshal(stored)
		s.memStore.SetConfig(r.Context(), caldav.KeyConfig, string(raw))
		if b.Password != "" {
			s.memStore.SetSecret(r.Context(), caldav.PasswordSecret, b.Password)
		}
		out := make([]map[string]interface{}, 0, len(cals))
		for _, c := range cals {
			out = append(out, map[string]interface{}{"path": c.Path, "name": c.Name, "components": c.SupportedComponentSet})
		}
		writeJSON(w, map[string]interface{}{"ok": true, "configured": true, "calendars": out})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
