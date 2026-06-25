package server

// REST endpoints for the Skills library so the Brain settings tab can list, read
// and delete the agent's saved procedures. Skills are file-backed (the agent
// writes them via the `skill` tool); this is read/curate from the UI.

import (
	"net/http"
	"path/filepath"

	"prism/internal/skills"
)

func (s *Server) skillsManager() *skills.Manager {
	return skills.NewManager(filepath.Join(s.cfg.WorkspaceDir, "skills"))
}

// GET /api/skills            -> { skills: [...] }   (full list)
// GET /api/skills?name=x     -> { skill: {...} }    (single)
// DELETE /api/skills?name=x  -> { ok: true }
func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	mgr := s.skillsManager()
	name := r.URL.Query().Get("name")

	switch r.Method {
	case "GET":
		if name != "" {
			sk, ok, err := mgr.Get(name)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if !ok {
				http.Error(w, "not found", 404)
				return
			}
			writeJSON(w, map[string]interface{}{"skill": sk})
			return
		}
		list, err := mgr.List()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if list == nil {
			list = []skills.Skill{}
		}
		writeJSON(w, map[string]interface{}{"skills": list})
	case "DELETE":
		if name == "" {
			http.Error(w, "name required", 400)
			return
		}
		if err := mgr.Delete(name); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
