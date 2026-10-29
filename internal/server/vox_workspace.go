package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"prism/internal/memory"
	"prism/internal/timeprefs"
)

const voxWorkspaceGroupsKey = "vox_workspace_groups"

func (s *Server) voxWorkspaceGroups(ctx context.Context) []int64 {
	var ids []int64
	if ms := s.store(); ms != nil {
		raw, _, _ := ms.GetConfig(ctx, voxWorkspaceGroupsKey)
		_ = json.Unmarshal([]byte(raw), &ids)
	}
	return ids
}
func (s *Server) voxGroupAllowed(ctx context.Context, gid int64) bool {
	for _, id := range s.voxWorkspaceGroups(ctx) {
		if id == gid {
			return true
		}
	}
	return false
}
func (s *Server) voxUserAllowed(ctx context.Context, u *memory.User) bool {
	if u == nil {
		return false
	}
	if !s.cfg.MultiUser || u.IsGlobalAdmin() {
		return true
	}
	if u.ID < 0 {
		return s.voxGroupAllowed(ctx, -u.ID)
	}
	for _, gid := range s.voxWorkspaceGroups(ctx) {
		if s.userInGroup(ctx, u.ID, gid) {
			return true
		}
	}
	return false
}

// Scopes are created here, never accepted from a browser or tool arguments.
// Shared group conversations can only read the standard's calls.
func (s *Server) voxReadScope(ctx context.Context, u *memory.User, shared bool) (map[string]any, error) {
	instance := s.voxInstanceID(ctx)
	if instance == "" {
		return nil, fmt.Errorf("archive identity unavailable")
	}
	scope := map[string]any{"instance": instance, "standard": s.voxUserAllowed(ctx, u)}
	if u != nil && u.ID >= 0 && !shared {
		scope["owner"] = u.ID
	}
	return scope, nil
}
func (s *Server) voxScopeHeader(ctx context.Context, session string) string {
	if s.cfg.VoxURL == "" {
		return ""
	}
	var u *memory.User
	if gs := groupScopeFromSessionID(session); gs != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(gs, "g"), 10, 64)
		u = &memory.User{ID: -id}
	} else if id := userIDFromSessionID(session); id > 0 {
		if ms := s.store(); ms != nil {
			u, _ = ms.GetUserByID(ctx, id)
		}
	} else if !s.cfg.MultiUser {
		u = serviceUser
	}
	scope, err := s.voxReadScope(ctx, u, u != nil && u.ID < 0)
	if err != nil {
		return `{"instance":"unavailable","standard":false}`
	}
	raw, _ := json.Marshal(scope)
	return string(raw)
}
func (s *Server) handleVoxWorkspaceAccess(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !u.IsGlobalAdmin() {
		writeErr(w, 403, "global admin only")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Groups []int64 `json:"groups"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			writeErr(w, 400, "invalid groups")
			return
		}
		groups, err := ms.ListGroups(r.Context())
		if err != nil {
			writeErr(w, 500, "could not load groups")
			return
		}
		valid := map[int64]bool{}
		for _, g := range groups {
			valid[g.ID] = true
		}
		selected := []int64{}
		seen := map[int64]bool{}
		for _, id := range body.Groups {
			if !valid[id] {
				writeErr(w, 400, "unknown group")
				return
			}
			if !seen[id] {
				selected = append(selected, id)
				seen[id] = true
			}
		}
		raw, _ := json.Marshal(selected)
		if err := ms.SetConfig(r.Context(), voxWorkspaceGroupsKey, string(raw)); err != nil {
			writeErr(w, 500, "could not save access")
			return
		}
	} else if r.Method != http.MethodGet {
		writeErr(w, 405, "GET or POST required")
		return
	}
	groups, _ := ms.ListGroups(r.Context())
	writeJSON(w, map[string]any{"groups": s.voxWorkspaceGroups(r.Context()), "availableGroups": groups})
}
func (s *Server) handleVoxWorkspace(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET required")
		return
	}
	allowed := s.voxUserAllowed(r.Context(), u)
	groups := []map[string]any{}
	if allowed && s.cfg.MultiUser && s.store() != nil {
		all, _ := s.store().ListGroups(r.Context())
		for _, g := range all {
			if s.voxGroupAllowed(r.Context(), g.ID) && (u.IsGlobalAdmin() || s.userInGroup(r.Context(), u.ID, g.ID)) {
				groups = append(groups, map[string]any{"id": g.ID, "name": g.Name})
			}
		}
	}
	writeJSON(w, map[string]any{"available": s.cfg.VoxURL != "" && allowed, "groups": groups, "multiUser": s.cfg.MultiUser, "admin": u != nil && (!s.cfg.MultiUser || u.IsGlobalAdmin())})
}

// The shared agent receives current access facts on every turn, independently
// of whether it opens prism_help. The recipe uses the existing execution tool.
func (s *Server) voxGroupArchiveContext(ctx context.Context, groupID int64) string {
	if s.cfg.VoxURL == "" {
		return ""
	}
	if !s.voxGroupAllowed(ctx, groupID) {
		return fmt.Sprintf("PrismVox is connected. Reception call archive access is NOT ENABLED for this shared group (id %d). A global administrator can enable the group in Telephony → Access. Do not claim access or send the user to a personal chat as a workaround. list_calls is an outbound-task queue, not the call archive. Older unclassified calls are only available in the private Administrator archive.", groupID)
	}
	if s.buildGroupAgentGuard(ctx, groupID)("exec_command", nil) != nil {
		return "PrismVox reception archive access is enabled for this group, but exec_command is restricted by its tool policy. An administrator must permit that existing tool under the applicable tool policy for the archive recipe. list_calls reads outbound tasks, not call archives."
	}
	return `PrismVox reception call archives are available to this shared group. Use exec_command for this authenticated read:
 curl --fail-with-body -sS --get "$PRISM_URL/api/vox/history" -H "Authorization: Bearer $PRISM_TOKEN" --data-urlencode "limit=20"
Add query parameters with --data-urlencode: since/until (ISO8601 with timezone), number, direction (inbound/outbound), query, offset, limit (max 50). Results contain the exact matching total and next_offset for the next page. id reads a call's turns; section=tools reads its tool results. Request dates and filters from the user's question. These scoped reads exclude personal calls; older unclassified calls stay in the private Administrator archive. list_calls is only the outbound-task queue; do not use it to test or read call archives. Do not infer that the archive is unavailable just because an outbound tool is restricted. Read prism_help(topic="call-history") for full parameters, pagination, errors and empty-result handling.`
}

// handleVoxWorkspaceAPI is an allow-list, not the administration proxy. It
// rewrites the scope, conversation id and target; credentials stay server-side.
func (s *Server) handleVoxWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !s.voxUserAllowed(r.Context(), u) {
		writeErr(w, 403, "workspace access denied")
		return
	}
	if s.cfg.VoxURL == "" {
		writeErr(w, 503, "PrismVox is disconnected")
		return
	}
	part := strings.TrimPrefix(r.URL.Path, "/api/vox-workspace/")
	target := ""
	method := r.Method
	switch part {
	case "history":
		if method == http.MethodGet {
			target = "history"
		}
	case "chat":
		if method == http.MethodPost {
			target = "chat"
		}
	case "conversation":
		if method == http.MethodGet {
			target = "supervision/conversation"
		}
	case "context":
		if method == http.MethodGet || method == http.MethodPost {
			target = "supervision/context"
		}
	}
	if target == "" {
		writeErr(w, 405, "operation unavailable")
		return
	}
	adminArchive := r.URL.Query().Get("group") == "admin"
	if adminArchive && !u.IsGlobalAdmin() {
		writeErr(w, 403, "admin archive only")
		return
	}
	gid, _ := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	shared := s.cfg.MultiUser
	if shared && !adminArchive {
		if gid > 0 {
			if !s.voxGroupAllowed(r.Context(), gid) || (!u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, gid)) {
				writeErr(w, 403, "group access denied")
				return
			}
		} else if !u.IsGlobalAdmin() {
			writeErr(w, 400, "group required")
			return
		}
	}
	scope, err := s.voxReadScope(r.Context(), u, shared)
	if err != nil {
		writeErr(w, 503, err.Error())
		return
	}
	if adminArchive {
		scope = map[string]any{"admin": true, "instance": s.voxInstanceID(r.Context())}
	}
	session := "owner"
	if shared {
		session = fmt.Sprintf("group-%d", gid)
	}
	if adminArchive {
		session = fmt.Sprintf("admin-%s-u%d", s.voxInstanceID(r.Context()), u.ID)
	}
	q := r.URL.Query()
	q.Del("group")
	q.Set("session_id", session)
	var body io.Reader
	if method == http.MethodPost {
		var in struct {
			Message string `json:"message"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		_, loc := timeprefs.Read(r.Context(), s.userStore(r))
		raw, _ := json.Marshal(map[string]any{"message": in.Message, "session_id": session, "timezone": loc.String()})
		body = bytes.NewReader(raw)
	}
	s.forwardVoxScoped(w, r, method, target, q, body, scope)
}

// The normal Prism agent can compose archive reads with its existing execution
// tool. Its signed token carries a user or group principal, never an admin proxy.
func (s *Server) handleVoxHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET required")
		return
	}
	u := currentUser(r)
	if u == nil {
		writeErr(w, 401, "unauthorized")
		return
	}
	if u.ID < 0 && !s.voxGroupAllowed(r.Context(), -u.ID) {
		writeErr(w, http.StatusForbidden, "Reception archive access is not enabled for this group. A global administrator can enable it in Telephony → Access.")
		return
	}
	scope, err := s.voxReadScope(r.Context(), u, u.ID < 0)
	if err != nil {
		writeErr(w, 503, err.Error())
		return
	}
	q := r.URL.Query()
	q.Set("kind", "calls")
	q.Del("session_id")
	s.forwardVoxScoped(w, r, http.MethodGet, "history", q, nil, scope)
}
func (s *Server) forwardVoxScoped(w http.ResponseWriter, r *http.Request, method, path string, q url.Values, body io.Reader, scope map[string]any) {
	if s.cfg.VoxURL == "" {
		writeErr(w, 503, "PrismVox is disconnected")
		return
	}
	target := strings.TrimRight(s.cfg.VoxURL, "/") + "/api/" + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(r.Context(), method, target, body)
	if err != nil {
		writeErr(w, 500, "request failed")
		return
	}
	raw, _ := json.Marshal(scope)
	req.Header.Set("X-Prism-Scope", string(raw))
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.VoxUser != "" {
		req.SetBasicAuth(s.cfg.VoxUser, s.cfg.VoxPassword)
	}
	// Supervision can make several model calls; do not apply the settings proxy's
	// thirty-second timeout to a conversation.
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		writeErr(w, 502, "PrismVox unavailable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleVoxWorkspacePage(w http.ResponseWriter, r *http.Request) {
	s.handleLegacyTelephonyPage(w, r)
}
