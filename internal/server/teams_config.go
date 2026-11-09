package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
)

func validTeamsAllowlist(raw string) bool {
	if len(raw) > 16384 {
		return false
	}
	for id := range parseAllowlist(raw) {
		if id != "*" && !teamsGUID.MatchString(id) {
			return false
		}
	}
	return true
}

func (s *Server) teamsAdminGroup(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if !s.cfg.MultiUser {
		writeErr(w, http.StatusNotFound, "Teams requires shared mode")
		return 0, false
	}
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return 0, false
	}
	if !s.isGroupAdminOf(r.Context(), currentUser(r), groupID) {
		writeErr(w, http.StatusForbidden, "group admin only")
		return 0, false
	}
	return groupID, true
}

func (s *Server) handleTeamsConfig(w http.ResponseWriter, r *http.Request) {
	groupID, ok := s.teamsAdminGroup(w, r)
	if !ok {
		return
	}
	m := s.teamsManager()
	if m == nil {
		writeErr(w, http.StatusServiceUnavailable, "Teams unavailable")
		return
	}
	m.configMu.Lock()
	defer m.configMu.Unlock()
	cfg, secret, err := s.teamsConfig(r.Context(), groupID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "group configuration unavailable")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]any{"configured": cfg.configured(secret), "config": cfg, "hasSecret": secret != "", "endpoint": teamsIncomingPrefix + strconv.FormatInt(groupID, 10)})
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		teamsConfig
		Secret     *string `json:"clientSecret"`
		Disconnect bool    `json:"disconnect"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ms := s.store()
	if body.Disconnect {
		if err := ms.SetSecret(r.Context(), teamsKey(teamsSecretKey, groupID), ""); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not disconnect bot")
			return
		}
		m.cancelGroup(groupID)
		writeJSON(w, map[string]any{"ok": true, "configured": false})
		return
	}
	next := body.teamsConfig
	next.AppID, next.TenantID = strings.ToLower(strings.TrimSpace(next.AppID)), strings.ToLower(strings.TrimSpace(next.TenantID))
	if !teamsGUID.MatchString(next.AppID) || !teamsGUID.MatchString(next.TenantID) {
		writeErr(w, http.StatusBadRequest, "App ID and tenant ID must be Microsoft GUIDs")
		return
	}
	if !validTeamsAllowlist(next.RAGRead) || !validTeamsAllowlist(next.RAGWrite) || !validTeamsAllowlist(next.Tools) {
		writeErr(w, http.StatusBadRequest, "Permissions must contain Entra user object IDs or *")
		return
	}
	if body.Secret != nil {
		secret = strings.TrimSpace(*body.Secret)
	} else if cfg.AppID != next.AppID || cfg.TenantID != next.TenantID {
		secret = ""
	}
	if secret == "" || len(secret) > 4096 {
		writeErr(w, http.StatusBadRequest, "A client secret is required when connecting or changing the bot identity")
		return
	}
	groups, err := ms.ListGroups(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot check bot ownership")
		return
	}
	for _, g := range groups {
		if g.ID == groupID {
			continue
		}
		other, _, err := s.teamsConfig(r.Context(), g.ID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "cannot check bot ownership")
			return
		}
		if strings.EqualFold(other.AppID, next.AppID) {
			writeErr(w, http.StatusConflict, "This bot App ID is already assigned to another Prism group")
			return
		}
	}
	if next.OwnerConversation != "" {
		refs, err := m.conversations(r.Context(), groupID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "cannot read conversations")
			return
		}
		found := false
		for _, ref := range refs {
			if ref.Conversation.ID == next.OwnerConversation && ref.Conversation.Type != "personal" && ref.AppID == next.AppID && ref.TenantID == next.TenantID {
				found = true
			}
		}
		if !found {
			writeErr(w, http.StatusBadRequest, "Choose a group conversation already used with this bot")
			return
		}
	}
	// Do not acknowledge a failed secret/config write. Keeping either value alone
	// cannot widen access: audience and tenant are independently checked inbound.
	if err := ms.SetSecret(r.Context(), teamsKey(teamsSecretKey, groupID), secret); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save client secret")
		return
	}
	data, _ := json.Marshal(next)
	if err := ms.SetConfig(r.Context(), teamsKey("teams_config", groupID), string(data)); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save configuration")
		return
	}
	m.cancelGroup(groupID)
	writeJSON(w, map[string]any{"ok": true, "configured": true})
}

// A ready-to-import app package avoids manual manifest editing. Icons are PNGs
// at the exact sizes required by Teams; no external asset fetch is necessary.
func (s *Server) handleTeamsManifest(w http.ResponseWriter, r *http.Request) {
	groupID, ok := s.teamsAdminGroup(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	cfg, secret, err := s.teamsConfig(r.Context(), groupID)
	if err != nil || !cfg.configured(secret) {
		writeErr(w, http.StatusBadRequest, "Configure the bot first")
		return
	}
	manifest := map[string]any{
		"$schema":         "https://developer.microsoft.com/en-us/json-schemas/teams/v1.20/MicrosoftTeams.schema.json",
		"manifestVersion": "1.20", "version": "1.0.0", "id": cfg.AppID,
		"name":        map[string]string{"short": "Prism", "full": "Prism shared agent"},
		"description": map[string]string{"short": "Talk to your Prism group's shared agent.", "full": "Use your Prism group's agent, knowledge base and authorized tools from Microsoft Teams."},
		"developer":   map[string]string{"name": "The Last Server", "websiteUrl": "https://www.thelastserver.com", "privacyUrl": "https://github.com/blazux/prism/blob/main/docs/help/shared-mode.md", "termsOfUseUrl": "https://github.com/blazux/prism/blob/main/LICENSE"},
		"icons":       map[string]string{"color": "color.png", "outline": "outline.png"}, "accentColor": "#6B8AFD",
		"bots":         []any{map[string]any{"botId": cfg.AppID, "scopes": []string{"personal", "team", "groupChat"}, "isNotificationOnly": false, "supportsFiles": true}},
		"validDomains": []string{},
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, _ := z.Create("manifest.json")
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.Encode(manifest)
	for _, icon := range []struct {
		name    string
		size    int
		outline bool
	}{{"color.png", 192, false}, {"outline.png", 32, true}} {
		img := image.NewNRGBA(image.Rect(0, 0, icon.size, icon.size))
		for y := 0; y < icon.size; y++ {
			for x := 0; x < icon.size; x++ {
				col := color.NRGBA{107, 138, 253, 255}
				if icon.outline {
					col = color.NRGBA{}
				}
				// A white P with a transparent counter, readable at 32 px.
				px, py := x*32/icon.size, y*32/icon.size
				if px >= 9 && px < 13 && py >= 6 && py < 26 || px >= 13 && px < 23 && py >= 6 && py < 10 || px >= 19 && px < 23 && py >= 10 && py < 16 || px >= 13 && px < 23 && py >= 16 && py < 20 {
					col = color.NRGBA{255, 255, 255, 255}
				}
				img.SetNRGBA(x, y, col)
			}
		}
		f, _ = z.Create(icon.name)
		png.Encode(f, img)
	}
	if err := z.Close(); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create app package")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="prism-teams-g%d.zip"`, groupID))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(buf.Bytes())
}
