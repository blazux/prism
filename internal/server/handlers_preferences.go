package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"prism/internal/memory"
)

const maxUIPreferenceBytes = 128 << 10

var preferenceIdentifier = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]*$`)
var preferenceWidth = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)px$`)

// These are native UI values, not an arbitrary agent_config read/write API.
var uiPreferenceKeys = map[string]bool{
	"prism-theme": true, "prism-custom-themes": true, "prism-ui-scale": true,
	"prism-density": true, "prism-bgfx": true, "prism-ui-font": true,
	"prism-chat-side": true, "prism-chat-default": true, "prism-window-snap": true,
	"active-workspace": true, "prism-workspace-icons": true, "chat-drawer-width": true,
	"prism-cal-mode": true, "prism-pinned-notes": true, "agent-config": true,
	"prism-approval":    true,
	"prism-admin-group": true, "vox-supervision-session": true,
}

func validPreferenceStrings(raw string, maxItems, maxLength int) bool {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil || len(values) > maxItems {
		return false
	}
	for _, value := range values {
		if len(value) == 0 || len(value) > maxLength {
			return false
		}
	}
	return true
}

func validUIPreference(key, value string) bool {
	if !uiPreferenceKeys[key] || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	oneOf := func(choices ...string) bool {
		for _, choice := range choices {
			if value == choice {
				return true
			}
		}
		return false
	}
	switch key {
	case "prism-theme", "active-workspace":
		return len(value) <= 256 && preferenceIdentifier.MatchString(value)
	case "vox-supervision-session":
		return len(value) <= 160 && preferenceIdentifier.MatchString(value)
	case "prism-admin-group":
		if value == "" {
			return true
		}
		id, err := strconv.ParseInt(value, 10, 64)
		return err == nil && id > 0
	case "prism-ui-scale":
		n, err := strconv.ParseFloat(value, 64)
		return err == nil && n >= 50 && n <= 200
	case "prism-bgfx":
		n, err := strconv.ParseFloat(value, 64)
		return err == nil && n >= 0 && n <= 100
	case "prism-density":
		return oneOf("compact", "comfortable", "spacious")
	case "prism-chat-side":
		return oneOf("left", "right")
	case "prism-chat-default":
		return oneOf("open", "closed")
	case "prism-window-snap":
		return oneOf("0", "1")
	case "prism-approval":
		return oneOf("auto", "manual")
	case "prism-cal-mode":
		return oneOf("month", "week", "day", "agenda")
	case "prism-ui-font":
		return len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00")
	case "chat-drawer-width":
		match := preferenceWidth.FindStringSubmatch(value)
		if match == nil {
			return false
		}
		n, _ := strconv.ParseFloat(match[1], 64)
		return n >= 200 && n <= 10000
	case "prism-pinned-notes":
		return validPreferenceStrings(value, 5000, 4096)
	case "prism-workspace-icons":
		var icons map[string]string
		if json.Unmarshal([]byte(value), &icons) != nil || icons == nil || len(icons) > 2048 {
			return false
		}
		for id, icon := range icons {
			if len(id) > 256 || !preferenceIdentifier.MatchString(id) || len(icon) > 128 {
				return false
			}
		}
		return true
	case "agent-config":
		var config map[string]json.RawMessage
		if json.Unmarshal([]byte(value), &config) != nil || config == nil {
			return false
		}
		for name, raw := range config {
			if name != "disabled" && name != "combinedToolNotices" {
				return false
			}
			if !validPreferenceStrings(string(raw), 2048, 256) {
				return false
			}
		}
		return true
	case "prism-custom-themes":
		var themes []struct {
			ID    string            `json:"id"`
			Label string            `json:"label"`
			Vars  map[string]string `json:"vars"`
		}
		if json.Unmarshal([]byte(value), &themes) != nil || themes == nil || len(themes) > 128 {
			return false
		}
		ids := make(map[string]bool)
		for _, theme := range themes {
			if len(theme.ID) > 256 || !preferenceIdentifier.MatchString(theme.ID) || ids[theme.ID] || len(theme.Label) > 512 || theme.Vars == nil || len(theme.Vars) > 256 {
				return false
			}
			ids[theme.ID] = true
			for name, token := range theme.Vars {
				if len(name) > 128 || (name != "color-scheme" && !strings.HasPrefix(name, "--")) || len(token) > 2048 {
					return false
				}
			}
		}
		return true
	}
	return false
}

func (s *Server) preferenceOwner(u *memory.User) string {
	if u.ID > 0 {
		return fmt.Sprintf("u%d", u.ID)
	}
	// Hosted personal environments use the same service identity. Bind the
	// browser's queued writes to this environment without exposing its token.
	sum := sha256.Sum256([]byte("prism-interface-owner\x00" + s.cfg.AuthToken + "\x00" + s.cfg.WorkspaceDir))
	return fmt.Sprintf("owner-%x", sum[:16])
}

func (s *Server) handlePreferences(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if u.ID < 0 {
		writeErr(w, http.StatusForbidden, "interface preferences require a personal identity")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPatch && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, PATCH, POST")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	owner := s.preferenceOwner(u)
	us := s.userStore(r)
	if us == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var values map[string]string
	var err error
	if r.Method == http.MethodGet {
		values, err = us.UIPreferences(r.Context())
	} else {
		var body struct {
			Values map[string]string `json:"values"`
			Owner  string            `json:"owner"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUIPreferenceBytes)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err = dec.Decode(&body); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeErr(w, http.StatusRequestEntityTooLarge, "interface preferences exceed the storage limit")
			} else {
				writeErr(w, http.StatusBadRequest, "invalid interface preferences")
			}
			return
		}
		if dec.Decode(new(any)) != io.EOF || body.Values == nil {
			writeErr(w, http.StatusBadRequest, "invalid interface preferences")
			return
		}
		if body.Owner != owner {
			writeErr(w, http.StatusConflict, "account changed; reload before saving preferences")
			return
		}
		for key, value := range body.Values {
			if !validUIPreference(key, value) {
				writeErr(w, http.StatusBadRequest, "invalid interface preference")
				return
			}
		}
		values, err = us.SaveUIPreferences(r.Context(), body.Values, r.Method == http.MethodPost, maxUIPreferenceBytes)
	}
	if errors.Is(err, memory.ErrUIPreferencesTooLarge) {
		writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "interface preferences unavailable")
		return
	}
	for key := range values {
		if !uiPreferenceKeys[key] {
			delete(values, key)
		}
	}
	_, hosted := r.Context().Value(resourceContextKey{}).(*Server)
	writeJSON(w, map[string]any{"values": values, "owner": owner, "legacyImportAllowed": u.ID == 0 && !hosted})
}
