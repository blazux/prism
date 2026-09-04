package server

// Server-implemented agent tools (see agent.ServerTool): webhooks, PIM
// sources and channels act on a signed-in user's own settings with the same
// validators the Settings pages use. Credentials never transit the chat: the
// agent stores them with request_secret first and passes the secret's NAME.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"prism/internal/agent"
	"prism/internal/caldav"
	"prism/internal/calendar"
	"prism/internal/memory"
	"prism/internal/notes"
	"prism/internal/oauthx"
	"prism/internal/tasks"
)

// serverToolsFor builds the per-user implementations. Nil when there is no
// store (the executor then reports the tools as unavailable).
func (s *Server) serverToolsFor(u *memory.User) map[string]agent.ServerTool {
	ms := s.store()
	if ms == nil {
		return nil
	}
	us, whScope := ms, "global"
	if u != nil && u.ID > 0 {
		us = ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
		whScope = fmt.Sprintf("u%d", u.ID)
	}
	return map[string]agent.ServerTool{
		"webhook":    s.webhookTool(ms, whScope),
		"pim_source": s.pimSourceTool(us),
		"channel":    s.channelTool(ms, us),
	}
}

func argStr(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return strings.TrimSpace(v)
}

func argBool(args map[string]any, k string) bool {
	v, _ := args[k].(bool)
	return v
}

// secretValue resolves a secret NAME (stored via request_secret) to its value,
// with a teaching error when it does not exist.
func secretValue(ctx context.Context, us *memory.Store, arg, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%s is required: ask the user for the value with request_secret(name=...) — it is stored without transiting the chat — then pass that secret's name here", arg)
	}
	v, ok, err := us.GetSecret(ctx, name)
	if err != nil {
		return "", err
	}
	if !ok || v == "" {
		return "", fmt.Errorf("secret %q not found — call request_secret(name=%q, ...) first, then retry with that name", name, name)
	}
	return v, nil
}

// ─── webhook ─────────────────────────────────────────────────────────────────

func (s *Server) webhookTool(ms *memory.Store, scope string) agent.ServerTool {
	describe := func(w memory.WebhookRow) string {
		state := "enabled"
		if !w.Enabled {
			state = "disabled"
		}
		sess := w.SessionID
		if sess == "" {
			sess = "its own webhook-" + w.ID + " session"
		}
		extra := ""
		if w.Deliver != "" {
			extra += ", deliver=" + w.Deliver
		}
		if w.Respond {
			extra += ", responds synchronously"
		}
		return fmt.Sprintf("- %s (id %s, %s, runs in %s%s)\n  POST <your Prism URL>%s%s  with header X-Prism-Token: %s", w.Name, w.ID, state, sess, extra, webhookIncomingPrefix, w.ID, w.Token)
	}
	return func(ctx context.Context, args map[string]any) (string, error) {
		switch argStr(args, "action") {
		case "list", "":
			rows, err := ms.WebhookList(ctx, scope)
			if err != nil {
				return "", err
			}
			if len(rows) == 0 {
				return "No webhooks yet. webhook action=add name=... prompt=... creates one (Settings → Webhooks shows the same list).", nil
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "Webhooks (%d). The caller must send the token as X-Prism-Token (or Bearer, or ?token=):\n", len(rows))
			for _, w := range rows {
				sb.WriteString(describe(w) + "\n")
			}
			return sb.String(), nil
		case "add":
			name := argStr(args, "name")
			if name == "" {
				return "", fmt.Errorf("name is required")
			}
			deliver := argStr(args, "deliver")
			switch deliver {
			case "", "telegram", "slack", "webex":
			default:
				return "", fmt.Errorf("deliver must be empty, telegram, slack or webex (got %q)", deliver)
			}
			row := memory.WebhookRow{
				ID: newWebhookID(), Scope: scope, Name: name, Token: newWebhookTok(),
				Prompt: argStr(args, "prompt"), Deliver: deliver, Respond: argBool(args, "respond"), Enabled: true,
			}
			if err := ms.WebhookUpsert(ctx, row); err != nil {
				return "", err
			}
			return "Webhook created. Give the caller this URL and token; it appears under Settings → Webhooks.\n" + describe(row) +
				"\nThe request body becomes the message ({{content}} in the prompt is replaced by it, otherwise it is appended).", nil
		case "remove":
			id := argStr(args, "id")
			if id == "" {
				return "", fmt.Errorf("id is required (from webhook action=list)")
			}
			existing, ok, _ := ms.WebhookByID(ctx, id)
			if !ok || existing.Scope != scope {
				rows, _ := ms.WebhookList(ctx, scope)
				var names []string
				for _, w := range rows {
					names = append(names, fmt.Sprintf("%s (id %s)", w.Name, w.ID))
				}
				if len(names) == 0 {
					return fmt.Sprintf("No webhook with id %q — you have none.", id), nil
				}
				return fmt.Sprintf("No webhook with id %q — nothing removed. Yours: %s", id, strings.Join(names, ", ")), nil
			}
			if err := ms.WebhookDelete(ctx, scope, id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Webhook %q (id %s) removed.", existing.Name, id), nil
		default:
			return "", fmt.Errorf("webhook: unknown action %q (expected list, add, remove)", argStr(args, "action"))
		}
	}
}

// ─── pim_source ──────────────────────────────────────────────────────────────

func (s *Server) pimSourceTool(us *memory.Store) agent.ServerTool {
	status := func(ctx context.Context) string {
		cal, _, _ := us.GetConfig(ctx, calendar.KeyProvider)
		tsk, _, _ := us.GetConfig(ctx, tasks.KeyProvider)
		if cal == "" {
			cal = "auto"
		}
		if tsk == "" {
			tsk = "auto"
		}
		_, caldavOK := caldav.Load(ctx, us)
		todo, _, _ := us.GetSecret(ctx, tasks.TodoistTokenSecret)
		np, _, _ := us.GetConfig(ctx, notes.KeyProvider)
		vault, _, _ := us.GetConfig(ctx, notes.KeyVaultPath)
		notesState := "built-in"
		if np == "vault" && vault != "" {
			notesState = "Markdown vault at " + vault
		}
		return fmt.Sprintf("PIM sources (Settings → Calendar / Notes):\n- calendar: %s → resolves to %s\n- tasks: %s → resolves to %s\n- available: CalDAV %v, Google %v, Microsoft %v, Todoist %v\n- notes: %s",
			cal, calendar.ProviderFor(ctx, us, agent.PIMScope).Kind(), tsk, tasks.ProviderFor(ctx, us, agent.PIMScope).Kind(),
			caldavOK, oauthx.Connected(ctx, us, "google"), oauthx.Connected(ctx, us, "microsoft"), todo != "", notesState)
	}
	return func(ctx context.Context, args map[string]any) (string, error) {
		action := argStr(args, "action")
		switch action {
		case "status", "":
			return status(ctx), nil
		case "set":
			var changed []string
			if v := argStr(args, "calendar"); v != "" {
				if !validSource(v, "google", "microsoft") {
					return "", fmt.Errorf("calendar must be auto, local, caldav, google or microsoft (got %q)", v)
				}
				if err := us.SetConfig(ctx, calendar.KeyProvider, v); err != nil {
					return "", err
				}
				changed = append(changed, "calendar="+v)
			}
			if v := argStr(args, "tasks"); v != "" {
				if !validSource(v, "todoist") {
					return "", fmt.Errorf("tasks must be auto, local, caldav or todoist (got %q)", v)
				}
				if err := us.SetConfig(ctx, tasks.KeyProvider, v); err != nil {
					return "", err
				}
				changed = append(changed, "tasks="+v)
			}
			if len(changed) == 0 {
				return "", fmt.Errorf("set needs calendar and/or tasks")
			}
			return "Updated " + strings.Join(changed, ", ") + ". Google/Microsoft/Todoist only take effect once that account is connected.\n" + status(ctx), nil
		case "connect_caldav":
			cfg := caldav.Config{URL: argStr(args, "url"), User: argStr(args, "user"), EventPath: argStr(args, "event_path"), TaskPath: argStr(args, "task_path")}
			if cfg.URL == "" || cfg.User == "" {
				return "", fmt.Errorf("url and user are required")
			}
			pass, err := secretValue(ctx, us, "password_secret", argStr(args, "password_secret"))
			if err != nil {
				return err.Error(), nil
			}
			cfg.Pass = pass
			cals, err := cfg.Discover(ctx)
			if err != nil {
				return fmt.Sprintf("CalDAV connection failed (nothing saved): %v", err), nil
			}
			if cfg.EventPath == "" || cfg.TaskPath == "" {
				ev, task := caldav.PickPaths(cals)
				if cfg.EventPath == "" {
					cfg.EventPath = ev
				}
				if cfg.TaskPath == "" {
					cfg.TaskPath = task
				}
			}
			stored := cfg
			stored.Pass = ""
			raw, _ := json.Marshal(stored)
			if err := us.SetConfig(ctx, caldav.KeyConfig, string(raw)); err != nil {
				return "", err
			}
			if err := us.SetSecret(ctx, caldav.PasswordSecret, pass); err != nil {
				return "", err
			}
			var names []string
			for _, c := range cals {
				names = append(names, c.Name)
			}
			return fmt.Sprintf("CalDAV connected (%d calendar(s): %s). Events from %s, tasks from %s. Settings → Calendar shows it; pim_source action=set calendar=caldav selects it explicitly.", len(cals), strings.Join(names, ", "), cfg.EventPath, cfg.TaskPath), nil
		case "disconnect_caldav":
			us.SetConfig(ctx, caldav.KeyConfig, "")
			us.SetSecret(ctx, caldav.PasswordSecret, "")
			return "CalDAV disconnected.", nil
		case "connect_todoist":
			tok, err := secretValue(ctx, us, "token_secret", argStr(args, "token_secret"))
			if err != nil {
				return err.Error(), nil
			}
			if err := validateTodoist(ctx, tok); err != nil {
				return fmt.Sprintf("Todoist rejected the token (nothing saved): %v", err), nil
			}
			if err := us.SetSecret(ctx, tasks.TodoistTokenSecret, tok); err != nil {
				return "", err
			}
			return "Todoist connected. pim_source action=set tasks=todoist makes it the active task source.", nil
		case "disconnect_todoist":
			us.SetSecret(ctx, tasks.TodoistTokenSecret, "")
			return "Todoist disconnected.", nil
		case "set_notes_vault":
			path := argStr(args, "path")
			if path == "" {
				return "", fmt.Errorf("path is required (an absolute directory reachable by the server container)")
			}
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				return fmt.Sprintf("%s is not a readable directory from the server (is it mounted into the prism-server container?) — nothing changed.", path), nil
			}
			if err := us.SetConfig(ctx, notes.KeyProvider, "vault"); err != nil {
				return "", err
			}
			if err := us.SetConfig(ctx, notes.KeyVaultPath, path); err != nil {
				return "", err
			}
			return "Notes now read from the Markdown vault at " + path + ".", nil
		case "use_builtin_notes":
			us.SetConfig(ctx, notes.KeyProvider, "local")
			return "Notes now use Prism's built-in store.", nil
		default:
			return "", fmt.Errorf("pim_source: unknown action %q (expected status, set, connect_caldav, disconnect_caldav, connect_todoist, disconnect_todoist, set_notes_vault, use_builtin_notes)", action)
		}
	}
}

// ─── channel ─────────────────────────────────────────────────────────────────

func (s *Server) channelTool(ms, us *memory.Store) agent.ServerTool {
	status := func(ctx context.Context) string {
		tg := "not configured — channel action=telegram_connect, or Settings → Channels → Telegram"
		if tgToken(us) != "" {
			if _, linked := tgAllowedChat(us); linked {
				tg = "bot connected and chat linked"
			} else {
				tg = "bot connected, no chat linked yet — open the bot in Telegram and send /start"
			}
		}
		slack := "not configured (a global admin sets it up in Settings → Channels)"
		if v, ok, _ := ms.GetSecret(ctx, slackBotTokenSecret); ok && v != "" {
			slack = "deployment bot connected"
		}
		return fmt.Sprintf("Channels:\n- Telegram: %s\n- Slack: %s\n- Webex: per group, configured by a group admin in the admin console → Shared agent", tg, slack)
	}
	return func(ctx context.Context, args map[string]any) (string, error) {
		switch action := argStr(args, "action"); action {
		case "status", "":
			return status(ctx), nil
		case "telegram_connect":
			tok, err := secretValue(ctx, us, "token_secret", argStr(args, "token_secret"))
			if err != nil {
				return err.Error(), nil
			}
			if err := telegramGetMe(ctx, tok); err != nil {
				return fmt.Sprintf("Telegram rejected the token (nothing saved): %v — check it with @BotFather.", err), nil
			}
			if err := us.SetSecret(ctx, tgTokenSecret, tok); err != nil {
				return "", err
			}
			us.SetConfig(ctx, tgAllowedChatKey, "") // new token = new bot → forget the previous chat
			s.startChannels()
			return "Telegram bot connected for this account. Next step for the user: open the bot in Telegram and send /start — the first chat that does so gets linked (Settings → Channels → Telegram shows the state).", nil
		case "telegram_unlink":
			us.SetConfig(ctx, tgAllowedChatKey, "")
			return "Telegram chat unlinked; the next /start links a chat again.", nil
		default:
			return "", fmt.Errorf("channel: unknown action %q (expected status, telegram_connect, telegram_unlink)", action)
		}
	}
}

// telegramGetMe validates a bot token against the Bot API before it is stored.
func telegramGetMe(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.telegram.org/bot"+token+"/getMe", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if !r.OK {
		if r.Description == "" {
			r.Description = resp.Status
		}
		return fmt.Errorf("%s", r.Description)
	}
	return nil
}
