package server

// User profile + avatar endpoints (Spectrum). Profiles hold the editable
// identity (display/first/last name, phone); avatars are small images for users
// and agents, stored in the DB and served with cache-busting via a version query.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxAvatarBytes = 1 << 20 // 1 MiB — clients downscale to ~256px before upload

var allowedAvatarMIME = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// ─── /api/profile ───────────────────────────────────────────────────────────────

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	switch r.Method {
	case "GET":
		p, err := ms.GetProfile(r.Context(), u.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, p)
	case "POST":
		var b struct {
			DisplayName string `json:"displayName"`
			FirstName   string `json:"firstName"`
			LastName    string `json:"lastName"`
			Phone       string `json:"phone"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		dn := strings.TrimSpace(b.DisplayName)
		if dn == "" {
			dn = strings.TrimSpace(b.FirstName + " " + b.LastName)
		}
		if dn == "" {
			dn = u.DisplayName
		}
		if err := ms.UpdateProfile(r.Context(), u.ID, dn, strings.TrimSpace(b.FirstName), strings.TrimSpace(b.LastName), strings.TrimSpace(b.Phone)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		p, _ := ms.GetProfile(r.Context(), u.ID)
		writeJSON(w, p)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// ─── /api/avatar ────────────────────────────────────────────────────────────────

// scopes: "u<id>" (user), "agent-u<id>" (personal agent), "agent-g<id>" (shared agent).
func (s *Server) canWriteAvatarScope(r *http.Request, scope string) bool {
	u := currentUser(r)
	if u == nil {
		return false
	}
	switch {
	case scope == fmt.Sprintf("u%d", u.ID), scope == fmt.Sprintf("agent-u%d", u.ID):
		return true
	case strings.HasPrefix(scope, "agent-g"):
		gid, err := strconv.ParseInt(strings.TrimPrefix(scope, "agent-g"), 10, 64)
		return err == nil && s.isGroupAdminOf(r.Context(), u, gid)
	}
	return false
}

func validAvatarScope(scope string) bool {
	if strings.HasPrefix(scope, "u") {
		_, err := strconv.ParseInt(scope[1:], 10, 64)
		return err == nil
	}
	if strings.HasPrefix(scope, "agent-u") || strings.HasPrefix(scope, "agent-g") {
		_, err := strconv.ParseInt(scope[7:], 10, 64)
		return err == nil
	}
	return false
}

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	scope := r.URL.Query().Get("scope")
	if !validAvatarScope(scope) {
		http.Error(w, "invalid scope", 400)
		return
	}
	switch r.Method {
	case "GET":
		data, mime, _, err := ms.GetAvatar(r.Context(), scope)
		if err != nil || len(data) == 0 {
			http.Error(w, "no avatar", 404)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Write(data)
	case "POST":
		if !s.canWriteAvatarScope(r, scope) {
			http.Error(w, "forbidden", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes+1024)
		if err := r.ParseMultipartForm(maxAvatarBytes + 1024); err != nil {
			http.Error(w, "file too large (max 1 MB)", 413)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file required", 400)
			return
		}
		defer file.Close()
		mime := hdr.Header.Get("Content-Type")
		if !allowedAvatarMIME[mime] {
			http.Error(w, "unsupported image type (png/jpeg/webp/gif only)", 415)
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, maxAvatarBytes+1))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if len(data) > maxAvatarBytes {
			http.Error(w, "file too large (max 1 MB)", 413)
			return
		}
		ver, err := ms.SetAvatar(r.Context(), scope, mime, data)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "avatarVer": ver})
	case "DELETE":
		if !s.canWriteAvatarScope(r, scope) {
			http.Error(w, "forbidden", 403)
			return
		}
		if err := ms.DeleteAvatar(r.Context(), scope); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
