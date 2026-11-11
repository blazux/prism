package server

// REST endpoints for the Skills library so the Brain settings tab can list, read
// and delete the agent's saved procedures. Skills are file-backed (the agent
// writes them via the `skill` tool); this is read/curate from the UI.

import (
	"encoding/json"
	"fmt"
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
	if s.cfg.MultiUser {
		u := currentUser(r)
		if u == nil || u.ID == 0 {
			writeErr(w, 403, "personal or group identity required")
			return
		}
		scope := fmt.Sprintf("u%d", u.ID)
		if u.ID < 0 {
			scope = fmt.Sprintf("g%d", -u.ID)
		}
		mgr = skills.NewManager(filepath.Join(s.cfg.WorkspaceDir, "skills", scope))
	}
	name := r.URL.Query().Get("name")

	switch r.Method {
	case http.MethodPatch:
		var b struct {
			Skill    skills.Skill `json:"skill"`
			Original string       `json:"original"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&b) != nil || b.Skill.Name == "" {
			writeErr(w, 400, "skill and original body required")
			return
		}
		old, exists, err := mgr.Get(b.Skill.Name)
		if err != nil {
			writeErr(w, 500, "could not read skill")
			return
		}
		if !exists {
			writeErr(w, 404, "skill not found")
			return
		}
		if old.Body != b.Original {
			writeErr(w, 409, "skill changed; reload before editing")
			return
		}
		if err := mgr.Save(b.Skill); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
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
