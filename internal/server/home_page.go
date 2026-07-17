package server

import "net/http"

// The workspace ("/") is the home. /home is kept only as a redirect so any old
// bookmark or link lands on the workspace instead of a separate launcher.
func (s *Server) handleHomePage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
}
