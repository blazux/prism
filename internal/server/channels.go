package server

// Messaging channels: external bridges (Telegram, Slack, …) that let the single
// owner chat with their Prism agent and receive cron / API deliveries. Each
// channel is an implementation of Channel; the server starts every configured
// one and routes deliveries by name.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Channel turns have no waiting HTTP client. Give complex work room to finish,
// while still bounding a stalled turn and keeping shutdown cancellation intact.
const channelTurnTimeout = time.Hour

func channelFailureMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "⏳ The task reached the one-hour limit and was stopped. You can ask the agent to continue."
	}
	return "⚠️ Sorry, something went wrong."
}

type Channel interface {
	Name() string
	Configured() bool
	Run(ctx context.Context) // receive loop until ctx is cancelled
	SendToOwner(text string) error
}

func (s *Server) initChannels() {
	s.channels = map[string]Channel{
		"telegram": &telegramManager{s: s},
		"slack":    &slackChannel{s: s},
		"webex":    &webexManager{s: s},
		"teams":    &teamsManager{s: s},
	}
}

// startChannels (re)launches the receive loop of every configured channel.
func (s *Server) startChannels() {
	s.stopChannels()
	ctx, cancel := context.WithCancel(s.runtimeContext())
	s.mu.Lock()
	s.chanCancel = cancel
	s.mu.Unlock()
	for _, ch := range s.channels {
		if ch.Configured() {
			c := ch
			s.background(func() {
				log.Printf("[%s] bridge started", c.Name())
				c.Run(ctx)
			})
		}
	}
}

func (s *Server) stopChannels() {
	s.mu.Lock()
	c := s.chanCancel
	s.chanCancel = nil
	s.mu.Unlock()
	if c != nil {
		c()
	}
}

// deliverToChannel sends a message to a named channel's owner (cron → channel,
// /api/chat deliver option).
func (s *Server) deliverToChannel(name, text string) error {
	ch, ok := s.channels[name]
	if !ok {
		return fmt.Errorf("unknown channel %q", name)
	}
	return ch.SendToOwner(text)
}

// deliverToChannelSession routes a delivery to the owner of a namespaced
// session ("u<id>-…") on channels that support per-user delivery (Telegram).
// Group channels resolve their destination from that session's identity.
func (s *Server) deliverToChannelSession(name, sessionID, text string) error {
	if name == "webex" {
		m, ok := s.channels[name].(*webexManager)
		if !ok {
			return fmt.Errorf("Webex unavailable")
		}
		gid, err := s.webexGroupForSession(s.runtimeContext(), sessionID)
		if err != nil {
			return err
		}
		return m.SendToGroup(gid, text)
	}
	if name == "teams" {
		ctx, cancel := context.WithTimeout(s.runtimeContext(), 30*time.Second)
		defer cancel()
		return s.deliverTeamsSession(ctx, sessionID, text)
	}
	if uid := userIDFromSession(sessionID); uid > 0 {
		if m, ok := s.channels[name].(*telegramManager); ok {
			return m.SendToUser(uid, text)
		}
	}
	return s.deliverToChannel(name, text)
}

// Resolve the destination from the task's identity, never from the set of
// configured bots. Personal users retain delivery to their own unique group.
func (s *Server) webexGroupForSession(ctx context.Context, session string) (int64, error) {
	if scope := groupScopeFromSessionID(session); scope != "" {
		gid, err := strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
		if err != nil || gid <= 0 {
			return 0, fmt.Errorf("invalid Webex group")
		}
		return gid, nil
	}
	if uid := userIDFromSession(session); uid > 0 {
		ms := s.store()
		if ms == nil {
			return 0, fmt.Errorf("database unavailable")
		}
		groups, err := ms.UserGroups(ctx, uid)
		if err != nil {
			return 0, err
		}
		if len(groups) == 1 {
			return groups[0].GroupID, nil
		}
	}
	return 0, fmt.Errorf("Webex delivery requires a group session or a personal account in exactly one group")
}

// Telegram can upload the snapshots emitted by add_attachment, including in
// scheduled /api/chat and webhook turns. Other channels retain text delivery.
func (s *Server) deliverResponseToChannel(ctx context.Context, name, sessionID, text string, files responseAttachments) error {
	if m, ok := s.channels[name].(*telegramManager); ok {
		api, chatID, err := m.destination(userIDFromSession(sessionID))
		if err != nil {
			return err
		}
		return s.tgSendResponse(ctx, api, chatID, sessionID, text, files)
	}
	return s.deliverToChannelSession(name, sessionID, text)
}
