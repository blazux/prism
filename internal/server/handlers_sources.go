package server

// /api/pim/sources — lets the user pick which connected provider backs the
// Calendar and Tasks apps (instead of relying on the silent auto precedence).

import (
	"encoding/json"
	"net/http"

	"prism/internal/caldav"
	"prism/internal/calendar"
	"prism/internal/oauthx"
	"prism/internal/tasks"
)

func (s *Server) handlePimSources(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	ctx := r.Context()
	switch r.Method {
	case "GET":
		cal, _, _ := s.memStore.GetConfig(ctx, calendar.KeyProvider)
		tsk, _, _ := s.memStore.GetConfig(ctx, tasks.KeyProvider)
		if cal == "" {
			cal = "auto"
		}
		if tsk == "" {
			tsk = "auto"
		}
		_, caldavOK := caldav.Load(ctx, s.memStore)
		todoTok, _, _ := s.memStore.GetSecret(ctx, tasks.TodoistTokenSecret)
		writeJSON(w, map[string]interface{}{
			"calendar": cal,
			"tasks":    tsk,
			"available": map[string]bool{
				"caldav":  caldavOK,
				"google":  oauthx.Connected(ctx, s.memStore, "google"),
				"todoist": todoTok != "",
			},
			// What each choice resolves to right now (so the UI can show "Auto → Google").
			"resolved": map[string]string{
				"calendar": calendar.ProviderFor(ctx, s.memStore, pimScope).Kind(),
				"tasks":    tasks.ProviderFor(ctx, s.memStore, pimScope).Kind(),
			},
		})
	case "POST":
		var b struct {
			Calendar string `json:"calendar"`
			Tasks    string `json:"tasks"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if validSource(b.Calendar, "google") {
			s.memStore.SetConfig(ctx, calendar.KeyProvider, b.Calendar)
		}
		if validSource(b.Tasks, "todoist") {
			s.memStore.SetConfig(ctx, tasks.KeyProvider, b.Tasks)
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// validSource accepts auto/local/caldav plus the domain-specific extra.
func validSource(v, extra string) bool {
	switch v {
	case "auto", "local", "caldav", extra:
		return true
	}
	return false
}
