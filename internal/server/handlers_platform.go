package server

// Platform configuration (global admin): which apps are enabled for users and
// which chat models are selectable. Stored as JSON arrays in agent_config so no
// new tables are needed.
//
//   - platform_disabled_apps   ["email", ...]        (empty/missing = all apps on)
//   - platform_allowed_models  ["qwen", ...]         (empty/missing = all models)
//
// Disabling an app hides it across the UI (rail, palette, settings tab). It is a
// deployment choice for a trusted team, not a security boundary — the underlying
// REST endpoints stay up.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const (
	keyDisabledApps  = "platform_disabled_apps"
	keyAllowedModels = "platform_allowed_models"
)

// knownApps is the set of rail apps a global admin can toggle.
var knownApps = []string{"email", "notes", "tasks", "calendar", "room"}

func (s *Server) platformList(ctx context.Context, key string) []string {
	ms := s.store()
	if ms == nil {
		return nil
	}
	raw, ok, err := ms.GetConfig(ctx, key)
	if err != nil || !ok || raw == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

// platformDisabledApps returns the disabled-app set (empty = everything enabled).
func (s *Server) platformDisabledApps(ctx context.Context) map[string]bool {
	set := map[string]bool{}
	for _, a := range s.platformList(ctx, keyDisabledApps) {
		set[a] = true
	}
	return set
}

// platformAllowedModels returns the global model allow-list. unrestricted is true
// when no list is configured (all models available).
func (s *Server) platformAllowedModels(ctx context.Context) (set map[string]bool, unrestricted bool) {
	list := s.platformList(ctx, keyAllowedModels)
	if len(list) == 0 {
		return nil, true
	}
	set = map[string]bool{}
	for _, m := range list {
		set[m] = true
	}
	return set, false
}

// GET /api/platform → what the UI needs to adapt for the current user.
func (s *Server) handlePlatform(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	disabled := s.platformList(r.Context(), keyDisabledApps)
	if disabled == nil {
		disabled = []string{}
	}
	writeJSON(w, map[string]interface{}{
		"disabledApps": disabled,
		// Vortex mode: this Cortex is docked with a Vox telephony stack, so the
		// Téléphonie app is available. Empty VoxURL = standalone Cortex, no telephony.
		"vortexMode": s.cfg.VoxURL != "",
	})
}

// GET/POST /api/admin/platform — global-admin-only management view.
func (s *Server) handleAdminPlatform(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !u.IsGlobalAdmin() {
		writeErr(w, http.StatusForbidden, "global admin only")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	switch r.Method {
	case "GET":
		disabled := s.platformList(r.Context(), keyDisabledApps)
		if disabled == nil {
			disabled = []string{}
		}
		allowed := s.platformList(r.Context(), keyAllowedModels)
		if allowed == nil {
			allowed = []string{}
		}
		// The unfiltered model list, so the admin can choose from everything.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		all, _ := s.chatModels(ctx)
		writeJSON(w, map[string]interface{}{
			"apps": knownApps, "disabledApps": disabled,
			"allModels": all, "allowedModels": allowed,
		})
	case "POST":
		var b struct {
			DisabledApps  *[]string `json:"disabledApps"`
			AllowedModels *[]string `json:"allowedModels"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.DisabledApps != nil {
			// Only known apps can be disabled — ignore anything else.
			valid := []string{}
			for _, a := range *b.DisabledApps {
				for _, k := range knownApps {
					if a == k {
						valid = append(valid, a)
						break
					}
				}
			}
			data, _ := json.Marshal(valid)
			if err := ms.SetConfig(r.Context(), keyDisabledApps, string(data)); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		if b.AllowedModels != nil {
			data, _ := json.Marshal(*b.AllowedModels)
			if err := ms.SetConfig(r.Context(), keyAllowedModels, string(data)); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		s.audit(r, "platform_config", map[string]interface{}{"disabledApps": b.DisabledApps, "allowedModels": b.AllowedModels})
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
