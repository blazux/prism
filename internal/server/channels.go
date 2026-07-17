package server

// Messaging channels: external bridges (Telegram, Slack, …) that let the single
// owner chat with their Prism agent and receive cron / API deliveries. Each
// channel is an implementation of Channel; the server starts every configured
// one and routes deliveries by name.

import (
	"context"
	"fmt"
	"log"
)

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
	}
}

// startChannels (re)launches the receive loop of every configured channel.
func (s *Server) startChannels() {
	s.stopChannels()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.chanCancel = cancel
	s.mu.Unlock()
	for _, ch := range s.channels {
		if ch.Configured() {
			go func(c Channel) {
				log.Printf("[%s] bridge started", c.Name())
				c.Run(ctx)
			}(ch)
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
// session ("u<id>-…") on channels that support per-user delivery (Telegram);
// otherwise falls back to the channel's single-owner path.
func (s *Server) deliverToChannelSession(name, sessionID, text string) error {
	if uid := userIDFromSession(sessionID); uid > 0 {
		if m, ok := s.channels[name].(*telegramManager); ok {
			return m.SendToUser(uid, text)
		}
	}
	return s.deliverToChannel(name, text)
}
