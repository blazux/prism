package server

// Slack bridge via Socket Mode: an outbound WebSocket (no public URL needed),
// mirroring the Telegram bridge. The owner DMs the bot; the first DM claims
// ownership (trust on first use) and further DMs from anyone else are refused.
// Needs a bot token (xoxb-…) and an app-level token (xapp-…, connections:write).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"prism/internal/memory"
)

const (
	slackBotTokenSecret = "slack_bot_token"
	slackAppTokenSecret = "slack_app_token"
	slackAllowedUserKey = "slack_allowed_user"
	slackOwnerChanKey   = "slack_owner_channel"
)

type slackChannel struct{ s *Server }

func (c *slackChannel) Name() string { return "slack" }

func (c *slackChannel) store() *memory.Store {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()
	return c.s.memStore
}

func (c *slackChannel) secret(key string) string {
	ms := c.store()
	if ms == nil {
		return ""
	}
	v, _, _ := ms.GetSecret(context.Background(), key)
	return strings.TrimSpace(v)
}

func (c *slackChannel) conf(key string) string {
	ms := c.store()
	if ms == nil {
		return ""
	}
	v, _, _ := ms.GetConfig(context.Background(), key)
	return v
}

func (c *slackChannel) setConf(key, val string) {
	if ms := c.store(); ms != nil {
		ms.SetConfig(context.Background(), key, val)
	}
}

func (c *slackChannel) Configured() bool {
	return c.secret(slackBotTokenSecret) != "" && c.secret(slackAppTokenSecret) != ""
}

func (c *slackChannel) Run(ctx context.Context) {
	bot, app := c.secret(slackBotTokenSecret), c.secret(slackAppTokenSecret)
	if bot == "" || app == "" {
		return
	}
	api := slack.New(bot, slack.OptionAppLevelToken(app))
	client := socketmode.New(api)
	go func() {
		if err := client.RunContext(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[slack] socketmode: %v", err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-client.Events:
			if evt.Type != socketmode.EventTypeEventsAPI {
				continue
			}
			if evt.Request != nil {
				client.Ack(*evt.Request)
			}
			ev, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok || ev.Type != slackevents.CallbackEvent {
				continue
			}
			if msg, ok := ev.InnerEvent.Data.(*slackevents.MessageEvent); ok {
				c.handleMessage(ctx, api, msg)
			}
		}
	}
}

func (c *slackChannel) handleMessage(ctx context.Context, api *slack.Client, ev *slackevents.MessageEvent) {
	// Only real user direct messages (ignore bots, edits, joins, channel posts).
	if ev.ChannelType != "im" || ev.BotID != "" || ev.SubType != "" || ev.User == "" {
		return
	}
	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return
	}
	allowed := c.conf(slackAllowedUserKey)
	if allowed == "" {
		c.setConf(slackAllowedUserKey, ev.User) // trust on first use
	} else if ev.User != allowed {
		api.PostMessage(ev.Channel, slack.MsgOptionText("⛔ This Prism agent is linked to another user.", false))
		return
	}
	c.setConf(slackOwnerChanKey, ev.Channel) // remember the DM channel for deliveries

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := c.s.runHeadlessChat(runCtx, "slack", text, "")
	if err != nil {
		log.Printf("[slack] chat: %v", err)
		api.PostMessage(ev.Channel, slack.MsgOptionText("⚠️ Sorry, something went wrong.", false))
		return
	}
	if strings.TrimSpace(resp) == "" {
		resp = "(no response)"
	}
	api.PostMessage(ev.Channel, slack.MsgOptionText(resp, false))
}

func (c *slackChannel) SendToOwner(text string) error {
	bot := c.secret(slackBotTokenSecret)
	if bot == "" {
		return fmt.Errorf("slack not configured")
	}
	api := slack.New(bot)
	channel := c.conf(slackOwnerChanKey)
	if channel == "" {
		user := c.conf(slackAllowedUserKey)
		if user == "" {
			return fmt.Errorf("no linked slack user — DM your bot first")
		}
		conv, _, _, err := api.OpenConversation(&slack.OpenConversationParameters{Users: []string{user}})
		if err != nil {
			return err
		}
		channel = conv.ID
		c.setConf(slackOwnerChanKey, channel)
	}
	_, _, err := api.PostMessage(channel, slack.MsgOptionText(text, false))
	return err
}

// GET /api/slack/config → {configured, linked}. POST {botToken, appToken} to set,
// or {disconnect:true} to clear.
func (s *Server) handleSlackConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	sc := &slackChannel{s: s}
	switch r.Method {
	case "GET":
		writeJSON(w, map[string]interface{}{
			"configured": sc.Configured(),
			"linked":     sc.conf(slackAllowedUserKey) != "",
		})
	case "POST":
		var b struct {
			BotToken   string `json:"botToken"`
			AppToken   string `json:"appToken"`
			Disconnect bool   `json:"disconnect"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Disconnect {
			ms.SetSecret(r.Context(), slackBotTokenSecret, "")
			ms.SetSecret(r.Context(), slackAppTokenSecret, "")
			ms.SetConfig(r.Context(), slackAllowedUserKey, "")
			ms.SetConfig(r.Context(), slackOwnerChanKey, "")
			s.startChannels()
			writeJSON(w, map[string]interface{}{"ok": true, "configured": false})
			return
		}
		bt, at := strings.TrimSpace(b.BotToken), strings.TrimSpace(b.AppToken)
		if bt == "" || at == "" {
			http.Error(w, "bot token and app token required", 400)
			return
		}
		ms.SetSecret(r.Context(), slackBotTokenSecret, bt)
		ms.SetSecret(r.Context(), slackAppTokenSecret, at)
		// New tokens → forget the previously linked user.
		ms.SetConfig(r.Context(), slackAllowedUserKey, "")
		ms.SetConfig(r.Context(), slackOwnerChanKey, "")
		s.startChannels()
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
