package server

import "net/http"

// Keep a single Room UI: the shell and direct links use the same client.
func (s *Server) handleRoomPage(w http.ResponseWriter, r *http.Request) {
	target := "/apps/room.html"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
