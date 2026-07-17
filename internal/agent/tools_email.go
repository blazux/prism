package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"prism/internal/email"
)

// emailStoredConfig is the non-secret part of the email setup (the password is
// kept separately in the encrypted secrets store under "email_password").
type emailStoredConfig struct {
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	User     string `json:"user"`
	From     string `json:"from"`
	Security string `json:"security,omitempty"` // "" / "ssl" | "starttls"
	Insecure bool   `json:"insecure,omitempty"` // accept self-signed cert (Proton Bridge)
}

const emailConfigKey = "email_config"
const emailPasswordSecret = "email_password"

func (e *ToolExecutor) loadEmailConfig(ctx context.Context) (email.Config, error) {
	if e.memStore == nil {
		return email.Config{}, fmt.Errorf("email unavailable: no database")
	}
	raw, ok, _ := e.userStore().GetConfig(ctx, emailConfigKey)
	if !ok || raw == "" {
		return email.Config{}, fmt.Errorf("email not configured — run email action=config first (imap_host, smtp_host, user, password)")
	}
	var sc emailStoredConfig
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		return email.Config{}, fmt.Errorf("corrupt email config: %w", err)
	}
	pass, _, _ := e.userStore().GetSecret(ctx, emailPasswordSecret)
	return email.Config{
		IMAPHost: sc.IMAPHost, IMAPPort: sc.IMAPPort,
		SMTPHost: sc.SMTPHost, SMTPPort: sc.SMTPPort,
		User: sc.User, From: sc.From, Pass: pass,
		Security: sc.Security, Insecure: sc.Insecure,
	}, nil
}

func (e *ToolExecutor) saveEmailConfig(ctx context.Context, sc emailStoredConfig, password string) error {
	b, _ := json.Marshal(sc)
	if err := e.userStore().SetConfig(ctx, emailConfigKey, string(b)); err != nil {
		return err
	}
	if password != "" {
		return e.userStore().SetSecret(ctx, emailPasswordSecret, password)
	}
	return nil
}

// emailTool implements the `email` tool.
func (e *ToolExecutor) emailTool(ctx context.Context, args map[string]interface{}) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("email unavailable: no database")
	}
	str := func(k string) string { v, _ := args[k].(string); return v }
	num := func(k string) int { f, _ := args[k].(float64); return int(f) }

	switch strings.ToLower(strings.TrimSpace(str("action"))) {
	case "config", "setup":
		// Merge over any existing config so partial updates work.
		var sc emailStoredConfig
		if raw, ok, _ := e.userStore().GetConfig(ctx, emailConfigKey); ok {
			json.Unmarshal([]byte(raw), &sc)
		}
		if v := str("imap_host"); v != "" {
			sc.IMAPHost = v
		}
		if v := num("imap_port"); v != 0 {
			sc.IMAPPort = v
		}
		if v := str("smtp_host"); v != "" {
			sc.SMTPHost = v
		}
		if v := num("smtp_port"); v != 0 {
			sc.SMTPPort = v
		}
		if v := str("user"); v != "" {
			sc.User = v
		}
		if v := str("from"); v != "" {
			sc.From = v
		}
		if v := str("security"); v != "" {
			sc.Security = v
		}
		if v, ok := args["insecure"].(bool); ok {
			sc.Insecure = v
		}
		if err := e.saveEmailConfig(ctx, sc, str("password")); err != nil {
			return "", err
		}
		return fmt.Sprintf("Email configured (imap=%s smtp=%s user=%s). Password %s.",
			sc.IMAPHost, sc.SMTPHost, sc.User,
			map[bool]string{true: "stored", false: "unchanged"}[str("password") != ""]), nil

	case "list", "inbox", "":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		limit := num("limit")
		msgs, err := cfg.List(limit)
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			return "Inbox is empty.", nil
		}
		return jsonResult(msgs), nil

	case "read", "open":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		uid := uint32(num("uid"))
		if uid == 0 {
			return "", fmt.Errorf("read requires a numeric uid (from email list)")
		}
		msg, err := cfg.Read(uid)
		if err != nil {
			return "", err
		}
		if len(msg.Body) > 8000 {
			msg.Body = msg.Body[:8000] + "\n…(truncated)"
		}
		return jsonResult(msg), nil

	case "search":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		q := str("query")
		if q == "" {
			return "", fmt.Errorf("search requires a query")
		}
		msgs, err := cfg.Search(q, num("limit"))
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			return "No matching messages.", nil
		}
		return jsonResult(msgs), nil

	case "send":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		to, subject, body := str("to"), str("subject"), str("body")
		if to == "" || body == "" {
			return "", fmt.Errorf("send requires 'to' and 'body'")
		}
		if err := cfg.Send(to, subject, body, "", nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("Email sent to %s.", to), nil

	case "reply":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		uid := uint32(num("uid"))
		body := str("body")
		if uid == 0 || body == "" {
			return "", fmt.Errorf("reply requires 'uid' (the message replied to) and 'body'")
		}
		orig, err := cfg.Read(uid)
		if err != nil {
			return "", fmt.Errorf("could not load original message: %w", err)
		}
		subject := orig.Subject
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
		to := str("to")
		if to == "" {
			to = orig.From
		}
		if err := cfg.Send(to, subject, body, orig.MessageID, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("Reply sent to %s.", to), nil

	default:
		return "", fmt.Errorf("email: unknown action %q (config, list, read, search, send, reply)", str("action"))
	}
}
