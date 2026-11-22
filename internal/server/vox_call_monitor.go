package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

func voxGatewayKey(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(raw, "/")))
	return hex.EncodeToString(sum[:])
}

var errVoxMissionPrincipalGone = errors.New("phone mission principal is no longer authorized")

// Voice session IDs are per-call, not identity-bearing. Bind the actual caller
// explicitly, and send later results to that caller's personal Prism assistant.
func (s *Server) configureVoiceVoxExecutor(e *agent.ToolExecutor, caller *memory.User) {
	e.SetVoxScopeProvider(func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if caller == nil {
			return `{"instance":"unavailable","standard":false}`
		}
		user := caller
		if s.cfg.MultiUser {
			if ms := s.store(); ms != nil {
				var err error
				user, err = ms.GetUserByID(ctx, caller.ID)
				if err != nil || user == nil || user.Status != memory.StatusApproved {
					return `{"instance":"unavailable","standard":false}`
				}
			}
		}
		scope, err := s.voxReadScope(ctx, user, false)
		if err != nil {
			return `{"instance":"unavailable","standard":false}`
		}
		raw, _ := json.Marshal(scope)
		return string(raw)
	})
	e.SetCallQueuedCallback(func(ctx context.Context, call agent.QueuedCall) error {
		if caller == nil {
			return fmt.Errorf("unknown caller")
		}
		session := assistantSession
		if s.cfg.MultiUser {
			session = fmt.Sprintf("u%d-%s", caller.ID, assistantSession)
		}
		return s.trackVoxCall(ctx, session, call)
	})
}

func (s *Server) voxMissionScope(ctx context.Context, session string) (map[string]any, int64, error) {
	ms := s.store()
	if ms == nil {
		return nil, 0, fmt.Errorf("database unavailable")
	}
	var user *memory.User
	var groupID int64
	if scope := groupScopeFromSessionID(session); scope != "" {
		groupID, _ = strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
		groups, err := ms.ListGroups(ctx)
		if err != nil {
			return nil, 0, err
		}
		exists := false
		for _, group := range groups {
			if group.ID == groupID {
				exists = true
			}
		}
		if !exists || !s.voxGroupAllowed(ctx, groupID) {
			return nil, 0, errVoxMissionPrincipalGone
		}
		user = &memory.User{ID: -groupID}
	} else if id := userIDFromSessionID(session); id > 0 {
		var err error
		user, err = ms.GetUserByID(ctx, id)
		if err != nil && !errors.Is(err, memory.ErrUserNotFound) {
			return nil, 0, err
		}
		if user == nil || user.Status != memory.StatusApproved {
			return nil, 0, errVoxMissionPrincipalGone
		}
	} else if !s.cfg.MultiUser {
		user = serviceUser
	} else {
		return nil, 0, errVoxMissionPrincipalGone
	}
	scope, err := s.voxReadScope(ctx, user, groupID > 0)
	return scope, groupID, err
}

type missionScope struct {
	Instance string `json:"instance"`
	Owner    *int64 `json:"owner"`
	Standard bool   `json:"standard"`
	Admin    bool   `json:"admin"`
}

func typedMissionScope(scope map[string]any) missionScope {
	raw, _ := json.Marshal(scope)
	var value missionScope
	_ = json.Unmarshal(raw, &value)
	return value
}

func sameMissionOwner(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (s *Server) trackVoxCall(ctx context.Context, session string, call agent.QueuedCall) error {
	ms := s.store()
	if ms == nil || call.ID <= 0 || session == "" || s.voxURL() == "" || voxGatewayKey(call.VoxURL) != voxGatewayKey(s.voxURL()) {
		return fmt.Errorf("phone mission tracking is unavailable")
	}
	expected, groupID, err := s.voxMissionScope(ctx, session)
	if err != nil {
		return err
	}
	identity := typedMissionScope(expected)
	var actual missionScope
	if json.Unmarshal([]byte(call.Scope), &actual) != nil || actual.Admin || actual.Instance != identity.Instance ||
		!sameMissionOwner(actual.Owner, identity.Owner) || groupID > 0 && !actual.Standard {
		return fmt.Errorf("phone mission identity does not match its conversation")
	}
	_, existed, err := ms.SessionOwner(ctx, session)
	if err != nil {
		return err
	}
	return ms.TrackVoxCall(ctx, memory.VoxCallMission{GatewayKey: voxGatewayKey(call.VoxURL), TaskID: call.ID,
		SessionID: session, PrismInstance: identity.Instance, OwnerID: identity.Owner, GroupID: groupID,
		SessionExisted: existed, PhoneNumber: call.PhoneNumber, ContactName: call.ContactName, Mission: call.Mission})
}

func (s *Server) startVoxCallMonitor() {
	s.background(func() {
		ctx := s.runtimeContext()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			s.pollVoxCallMissions(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

type monitoredCall struct {
	ID      int64   `json:"id"`
	Status  string  `json:"status"`
	Summary *string `json:"result_summary"`
}

func (s *Server) pollVoxCallMissions(ctx context.Context) {
	ms := s.store()
	base, user, password := s.voxCredentials()
	if ms == nil || base == "" || ctx.Err() != nil {
		return
	}
	calls, err := ms.PendingVoxCalls(ctx, voxGatewayKey(base), 50)
	if err != nil {
		log.Printf("[telephony] cannot read followed missions: %v", err)
		return
	}
	for _, call := range calls {
		if ctx.Err() != nil || s.voxURL() != base {
			return
		}
		scope, _, err := s.voxMissionScope(ctx, call.SessionID)
		if err != nil {
			// Do not read private results for a removed account or revoked group.
			if errors.Is(err, errVoxMissionPrincipalGone) {
				_ = ms.CheckVoxCall(ctx, call, true)
			} else {
				return // a transient database failure must not abandon the mission
			}
			continue
		}
		current := typedMissionScope(scope)
		if current.Instance != call.PrismInstance || !sameMissionOwner(current.Owner, call.OwnerID) {
			_ = ms.CheckVoxCall(ctx, call, true)
			continue
		}
		if call.SessionExisted {
			_, exists, err := ms.SessionOwner(ctx, call.SessionID)
			if err != nil {
				return
			}
			if !exists {
				_ = ms.CheckVoxCall(ctx, call, true)
				continue
			}
		}
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(base, "/")+fmt.Sprintf("/api/calls/%d", call.TaskID), nil)
		if err != nil {
			cancel()
			return
		}
		raw, _ := json.Marshal(scope)
		req.Header.Set("X-Prism-Scope", string(raw))
		if user != "" {
			req.SetBasicAuth(user, password)
		}
		response, err := voxControlHTTP.Do(req)
		if err != nil {
			cancel()
			return // one failed connection must not repeat for every followed ID
		}
		var item monitoredCall
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&item)
		_ = response.Body.Close()
		cancel()
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode >= 500 {
			return // keep pending: credentials or the phone service can recover
		}
		if response.StatusCode == http.StatusNotFound {
			item = monitoredCall{ID: call.TaskID, Status: "unavailable"}
		} else if response.StatusCode != http.StatusOK || decodeErr != nil || item.ID != call.TaskID {
			_ = ms.CheckVoxCall(ctx, call, false)
			continue
		}
		switch item.Status {
		case "done", "failed", "cancelled", "unavailable":
			s.publishVoxCallCompletion(ctx, call, item)
		default:
			_ = ms.CheckVoxCall(ctx, call, false)
		}
	}
}

func clippedCallText(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}

func (s *Server) publishVoxCallCompletion(ctx context.Context, call memory.VoxCallMission, item monitoredCall) {
	who := call.ContactName
	if who == "" {
		who = call.PhoneNumber
	}
	title := fmt.Sprintf("Phone call #%d — %s", call.TaskID, item.Status)
	message := fmt.Sprintf("Phone-system result for mission #%d to %s: %s.\nMission: %s", call.TaskID, who, item.Status, clippedCallText(call.Mission, 1000))
	if item.Summary != nil && strings.TrimSpace(*item.Summary) != "" {
		message += "\nRecorded summary (call data): " + clippedCallText(*item.Summary, 8000)
	}
	message += "\nThe call status alone does not establish that the mission succeeded."
	level := "info"
	if item.Status == "failed" || item.Status == "unavailable" {
		level = "warning"
	}
	author := "Assistant"
	if call.GroupID > 0 {
		if cfg, err := s.store().GetRoomConfig(ctx, call.GroupID); err == nil && cfg.AgentName != "" {
			author = cfg.AgentName
		}
	}
	// Strict providers require tool results immediately after their assistant
	// batch. Defer external assistant rows until this conversation is idle; the
	// existing admission lock also prevents a new turn racing the transaction.
	s.runsMu.Lock()
	busy := s.agentJobsLocked(call.SessionID) > 0 || s.deletingSessions[call.SessionID]
	for _, run := range s.runs {
		if run.owner != nil && run.owner.sessionID == call.SessionID {
			busy = true
		}
	}
	if busy {
		s.runsMu.Unlock()
		_ = s.store().CheckVoxCall(ctx, call, false)
		return
	}
	completion, err := s.store().CompleteVoxCall(ctx, call, item.Status, title, message, level, author)
	s.runsMu.Unlock()
	if err != nil {
		log.Printf("[telephony] cannot save mission %d result: %v", call.TaskID, err)
		return
	}
	if completion == nil {
		return
	}
	s.mu.RLock()
	var agents []*agent.Agent
	for client := range s.clients {
		if client.sessionID == call.SessionID && client.ag != nil {
			agents = append(agents, client.ag)
		}
	}
	s.mu.RUnlock()
	for _, current := range agents {
		current.ObservePersistedMessage(completion.MessageID, "assistant", message)
	}
	s.pushJSONToSession(call.SessionID, map[string]any{"type": "conversation_event", "id": completion.MessageID,
		"role": "assistant", "content": message, "session": call.SessionID, "createdAt": completion.CreatedAt.Format(time.RFC3339)})
	if call.OwnerID != nil && *call.OwnerID > 0 {
		s.pushNotificationToUser(*call.OwnerID, completion.NotificationID, title, message, level)
	} else {
		s.pushNotificationToSession(call.SessionID, completion.NotificationID, title, message, level)
	}
	if completion.RoomMessage != nil && s.rooms != nil {
		s.rooms.broadcast(call.GroupID, roomEvent(*completion.RoomMessage))
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.deliverVoxCallResult(deliveryCtx, call.SessionID, message); err != nil {
		log.Printf("[telephony] mission %d result saved; channel delivery unavailable: %v", call.TaskID, err)
	}
}

// Automatic delivery never substitutes a group's announcement target for the
// conversation that requested the call. Unknown/private references remain in
// Prism's persisted conversation and notification feed.
func (s *Server) deliverVoxCallResult(ctx context.Context, session, message string) error {
	uid := userIDFromSessionID(session)
	if session == "telegram" || uid > 0 && session == fmt.Sprintf("u%d-telegram", uid) {
		return s.deliverResponseToChannel(ctx, "telegram", session, message, nil)
	}
	if session == "slack" {
		return s.deliverToChannelSession("slack", session, message)
	}
	if strings.HasPrefix(session, "webex-g") {
		gid, err := s.webexGroupForSession(ctx, session)
		if err != nil {
			return err
		}
		channel := &webexChannel{s: s, groupID: gid}
		token := channel.token()
		if token == "" {
			return fmt.Errorf("Webex is not configured for this group")
		}
		var page struct {
			Items []webexRoom `json:"items"`
		}
		if err := channel.apiGet(ctx, token, "/rooms?max=1000", &page); err != nil {
			return err
		}
		var target string
		for _, room := range page.Items {
			if fmt.Sprintf("webex-g%d-%s", gid, sanitizeSessionID(room.ID)) == session {
				if target != "" {
					return fmt.Errorf("ambiguous original Webex conversation")
				}
				target = room.ID
			}
		}
		if target == "" {
			return fmt.Errorf("original Webex conversation is unavailable")
		}
		return channel.postMarkdown(ctx, target, message)
	}
	if strings.HasPrefix(session, "teams-g") {
		manager := s.teamsManager()
		if manager == nil {
			return fmt.Errorf("Teams unavailable")
		}
		scope := groupScopeFromSessionID(session)
		gid, _ := strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
		cfg, secret, err := s.teamsConfig(ctx, gid)
		if err != nil || !cfg.configured(secret) {
			return fmt.Errorf("Teams configuration unavailable")
		}
		refs, err := manager.conversations(ctx, gid)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			activity := teamsActivity{ID: ref.ActivityID, ServiceURL: ref.ServiceURL, Recipient: ref.Bot, Conversation: ref.Conversation}
			if ref.AppID == cfg.AppID && ref.TenantID == cfg.TenantID && teamsSessionID(gid, activity) == session {
				return manager.reply(ctx, cfg, secret, activity, message)
			}
		}
		return fmt.Errorf("original Teams conversation is unavailable")
	}
	return nil
}
