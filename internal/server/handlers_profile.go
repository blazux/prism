package server

// User profile + avatar endpoints (Spectrum). Profiles hold the editable
// identity (display/first/last name, phone); avatars are small images for users
// and agents, stored in the DB and served with cache-busting via a version query.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"prism/internal/memory"
	"prism/internal/timeprefs"
)

// profileResponse is the profile plus what the page needs to render the
// default-group picker: the groups to choose from, and which one currently
// speaks for this user (their pick, or the automatic fallback).
type profileResponse struct {
	memory.Profile
	Groups         []memory.Membership `json:"groups"`
	DefaultGroupID int64               `json:"defaultGroupId"`
}

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
	// The single-user identity (id 0) has no users row, so the UPDATE below
	// would silently match nothing and the page would say "Saved" over a no-op
	// (lived it). Its profile lives in config keys instead.
	if u.ID == 0 {
		s.handleServiceProfile(w, r, ms)
		return
	}
	switch r.Method {
	case "GET":
		p, err := ms.GetProfile(r.Context(), u.ID)
		if err != nil {
			// Brand-new user without a profile row yet — must not 500: return a
			// default so the profile page and the avatar scope (u<id>) still work.
			p = memory.Profile{UserID: u.ID, DisplayName: u.DisplayName}
		}
		// The groups come with the profile so the page can offer the default-group
		// picker without a second round trip, and hide it entirely for the many
		// users who are in one group or none.
		s.profileTimezone(r, &p)
		groups, _ := ms.UserGroups(r.Context(), u.ID)
		defaultGroup, _ := ms.DefaultGroupID(r.Context(), u.ID)
		writeJSON(w, profileResponse{Profile: p, Groups: groups, DefaultGroupID: defaultGroup})
	case "POST":
		var b struct {
			DisplayName string  `json:"displayName"`
			FirstName   string  `json:"firstName"`
			LastName    string  `json:"lastName"`
			Phone       string  `json:"phone"`
			Email       *string `json:"email"`
			Timezone    *string `json:"timezone"`
			// Empty = leave the transfer preference alone. A client that does not
			// know about the field must not silently reset it to blind.
			Transfer string `json:"transfer"`
			// nil = leave the default group alone; 0 = clear it and go back to the
			// automatic one. The pointer is what tells those two apart.
			DefaultGroupID *int64 `json:"defaultGroupId"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Timezone != nil {
			*b.Timezone = strings.TrimSpace(*b.Timezone)
			if err := timeprefs.Validate(*b.Timezone); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		}
		if !validProfileEmail(b.Email) {
			writeErr(w, http.StatusBadRequest, "Enter a valid email address.")
			return
		}
		dn := strings.TrimSpace(b.DisplayName)
		if dn == "" {
			dn = strings.TrimSpace(b.FirstName + " " + b.LastName)
		}
		if dn == "" {
			dn = u.DisplayName
		}
		if t := strings.TrimSpace(b.Transfer); t != "" {
			if err := ms.SetTransferType(r.Context(), u.ID, t); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		if b.DefaultGroupID != nil {
			if err := ms.SetDefaultGroup(r.Context(), u.ID, *b.DefaultGroupID); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := ms.UpdateProfile(r.Context(), u.ID, dn, strings.TrimSpace(b.FirstName), strings.TrimSpace(b.LastName), strings.TrimSpace(b.Phone), b.Email); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if b.Timezone != nil {
			if err := s.userStore(r).SetConfig(r.Context(), timeprefs.Key, *b.Timezone); err != nil {
				writeErr(w, 500, "cannot save timezone")
				return
			}
		}
		p, _ := ms.GetProfile(r.Context(), u.ID)
		s.profileTimezone(r, &p)
		groups, _ := ms.UserGroups(r.Context(), u.ID)
		defaultGroup, _ := ms.DefaultGroupID(r.Context(), u.ID)
		writeJSON(w, profileResponse{Profile: p, Groups: groups, DefaultGroupID: defaultGroup})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// Config keys backing the single-user profile (no users row to update).
const (
	cfgProfileDisplay = "profile_display_name"
	cfgProfileFirst   = "profile_first_name"
	cfgProfileLast    = "profile_last_name"
	cfgProfilePhone   = "profile_phone"
	cfgProfileEmail   = "profile_email"
)

func (s *Server) handleServiceProfile(w http.ResponseWriter, r *http.Request, ms *memory.Store) {
	get := func(key string) string { v, _, _ := ms.GetConfig(r.Context(), key); return v }
	profile := func() memory.Profile {
		zone, loc := timeprefs.Read(r.Context(), ms)
		return memory.Profile{Timezone: zone, EffectiveTimezone: loc.String(),
			DisplayName: get(cfgProfileDisplay), FirstName: get(cfgProfileFirst),
			LastName: get(cfgProfileLast), Phone: get(cfgProfilePhone), Email: get(cfgProfileEmail),
			AvatarVer: ms.AvatarVer(r.Context(), "u0"),
		}
	}
	switch r.Method {
	case "GET":
		writeJSON(w, profile())
	case "POST":
		var b struct {
			DisplayName string  `json:"displayName"`
			FirstName   string  `json:"firstName"`
			LastName    string  `json:"lastName"`
			Phone       string  `json:"phone"`
			Email       *string `json:"email"`
			Timezone    *string `json:"timezone"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Timezone != nil {
			*b.Timezone = strings.TrimSpace(*b.Timezone)
			if err := timeprefs.Validate(*b.Timezone); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		}
		if !validProfileEmail(b.Email) {
			writeErr(w, http.StatusBadRequest, "Enter a valid email address.")
			return
		}
		dn := strings.TrimSpace(b.DisplayName)
		if dn == "" {
			dn = strings.TrimSpace(b.FirstName + " " + b.LastName)
		}
		values := map[string]string{
			cfgProfileDisplay: dn, cfgProfileFirst: strings.TrimSpace(b.FirstName),
			cfgProfileLast: strings.TrimSpace(b.LastName), cfgProfilePhone: strings.TrimSpace(b.Phone),
		}
		if b.Timezone != nil {
			values[timeprefs.Key] = *b.Timezone
		}
		if b.Email != nil {
			values[cfgProfileEmail] = *b.Email
		}
		for key, val := range values {
			if err := ms.SetConfig(r.Context(), key, val); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		writeJSON(w, profile())
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

// An omitted email preserves the stored value; an empty email clears it.
func validProfileEmail(email *string) bool {
	if email == nil {
		return true
	}
	*email = strings.TrimSpace(*email)
	if *email == "" {
		return true
	}
	if len(*email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(*email)
	return err == nil && addr.Address == *email && strings.Contains(addr.Address, "@")
}

func (s *Server) profileTimezone(r *http.Request, p *memory.Profile) {
	if store := s.userStore(r); store != nil {
		zone, loc := timeprefs.Read(r.Context(), store)
		p.Timezone = zone
		p.EffectiveTimezone = loc.String()
	}
}
