package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type teamsReference struct {
	AppID        string            `json:"appId"`
	TenantID     string            `json:"tenantId"`
	Conversation teamsConversation `json:"conversation"`
	ServiceURL   string            `json:"serviceUrl"`
	Bot          teamsAccount      `json:"bot"`
	ActivityID   string            `json:"activityId"`
}

func (m *teamsManager) conversations(ctx context.Context, groupID int64) ([]teamsReference, error) {
	ms := m.s.store()
	if ms == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	raw, _, err := ms.GetConfig(ctx, teamsKey("teams_conversations", groupID))
	if err != nil {
		return nil, err
	}
	refs := []teamsReference{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &refs); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func (m *teamsManager) rememberConversation(ctx context.Context, groupID int64, cfg teamsConfig, a teamsActivity) {
	// Never offer someone's private chat as a destination for group jobs.
	if a.Conversation.Type == "personal" {
		return
	}
	m.configMu.Lock()
	defer m.configMu.Unlock()
	refs, err := m.conversations(ctx, groupID)
	if err != nil {
		return
	}
	ref := teamsReference{cfg.AppID, cfg.TenantID, a.Conversation, a.ServiceURL, a.Recipient, a.ID}
	kept := []teamsReference{ref}
	for _, old := range refs {
		if old.AppID == cfg.AppID && old.TenantID == cfg.TenantID && old.Conversation.ID != a.Conversation.ID && len(kept) < 100 {
			kept = append(kept, old)
		}
	}
	data, _ := json.Marshal(kept)
	m.s.store().SetConfig(ctx, teamsKey("teams_conversations", groupID), string(data))
}

func (s *Server) handleTeamsConversations(w http.ResponseWriter, r *http.Request) {
	groupID, ok := s.teamsAdminGroup(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	m := s.teamsManager()
	if m == nil {
		writeErr(w, http.StatusServiceUnavailable, "Teams unavailable")
		return
	}
	cfg, _, err := s.teamsConfig(r.Context(), groupID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "configuration unavailable")
		return
	}
	refs, err := m.conversations(r.Context(), groupID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "conversations unavailable")
		return
	}
	items := []map[string]string{}
	for _, ref := range refs {
		if ref.AppID != cfg.AppID || ref.TenantID != cfg.TenantID || ref.Conversation.Type == "personal" {
			continue
		}
		name := ref.Conversation.Name
		if name == "" {
			name = ref.Conversation.Type + " · " + ref.Conversation.ID
		}
		items = append(items, map[string]string{"id": ref.Conversation.ID, "name": name})
	}
	writeJSON(w, map[string]any{"conversations": items})
}

func (m *teamsManager) SendToGroup(ctx context.Context, groupID int64, text string) error {
	cfg, secret, err := m.s.teamsConfig(ctx, groupID)
	if err != nil || !cfg.configured(secret) {
		return fmt.Errorf("Teams is not configured for group %d", groupID)
	}
	if cfg.OwnerConversation == "" {
		return fmt.Errorf("choose a Teams announcement conversation in Admin → Shared agent")
	}
	refs, err := m.conversations(ctx, groupID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.AppID == cfg.AppID && ref.TenantID == cfg.TenantID && ref.Conversation.ID == cfg.OwnerConversation && ref.Conversation.Type != "personal" {
			a := teamsActivity{ID: ref.ActivityID, ServiceURL: ref.ServiceURL, Recipient: ref.Bot, Conversation: ref.Conversation}
			a.From = teamsAccount{}
			return m.reply(ctx, cfg, secret, a, text)
		}
	}
	return fmt.Errorf("announcement conversation is no longer available; mention the bot there and select it again")
}

// No implicit fan-out: a delivery without a group is safe only if exactly one
// group has an announcement target. Session-aware deliveries always name it.
func (m *teamsManager) SendToOwner(text string) error {
	ctx, cancel := context.WithTimeout(m.s.runtimeContext(), 30*time.Second)
	defer cancel()
	ms := m.s.store()
	if ms == nil {
		return fmt.Errorf("database unavailable")
	}
	groups, err := ms.ListGroups(ctx)
	if err != nil {
		return err
	}
	var target int64
	for _, g := range groups {
		cfg, secret, err := m.s.teamsConfig(ctx, g.ID)
		if err != nil {
			return err
		}
		if cfg.configured(secret) && cfg.OwnerConversation != "" {
			if target != 0 {
				return fmt.Errorf("Teams delivery requires a group-scoped session when multiple groups are configured")
			}
			target = g.ID
		}
	}
	if target == 0 {
		return fmt.Errorf("no Teams announcement conversation configured")
	}
	return m.SendToGroup(ctx, target, text)
}

func (s *Server) deliverTeamsSession(ctx context.Context, session, text string) error {
	m := s.teamsManager()
	if m == nil {
		return fmt.Errorf("Teams unavailable")
	}
	if scope := groupScopeFromSessionID(session); scope != "" {
		groupID, err := strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
		if err != nil {
			return err
		}
		return m.SendToGroup(ctx, groupID, text)
	}
	// Personal sessions are deliberately not routed to a group's public room.
	return fmt.Errorf("Teams delivery requires a group-scoped session (Room, Webex or Teams)")
}
