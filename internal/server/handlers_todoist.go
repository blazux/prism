package server

// REST config for the Todoist task backend. The personal API token is stored in
// the encrypted secrets store; when present, tasks come from Todoist.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"prism/internal/tasks"
)

func (s *Server) handleTodoistConfig(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	switch r.Method {
	case "GET":
		tok, _, _ := s.memStore.GetSecret(r.Context(), tasks.TodoistTokenSecret)
		writeJSON(w, map[string]interface{}{"configured": tok != ""})
	case "POST":
		var b struct {
			Token      string `json:"token"`
			Disconnect bool   `json:"disconnect"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Disconnect {
			s.memStore.SetSecret(r.Context(), tasks.TodoistTokenSecret, "")
			writeJSON(w, map[string]interface{}{"ok": true, "configured": false})
			return
		}
		b.Token = strings.TrimSpace(b.Token)
		if b.Token == "" {
			http.Error(w, "token required", 400)
			return
		}
		// Validate by listing tasks with the supplied token.
		if err := validateTodoist(r.Context(), b.Token); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.memStore.SetSecret(r.Context(), tasks.TodoistTokenSecret, b.Token); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "configured": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func validateTodoist(ctx context.Context, token string) error {
	p := tasks.NewTodoistProvider(token)
	_, err := p.List(ctx, false)
	return err
}
