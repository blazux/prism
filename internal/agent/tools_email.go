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
	// ListLimit is the Email app's inbox page size (Settings → Email). The agent
	// never reads it, but it must round-trip through this struct: a config
	// action re-marshals the whole record, and a missing field here silently
	// reset the user's choice to the default.
	ListLimit int `json:"list_limit,omitempty"`
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
	if err := email.ValidateBridgePreset(e.userStore().LocalMailBridgeDisabled, sc.IMAPHost, sc.SMTPHost); err != nil {
		return err
	}
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
		if v := strings.ToLower(strings.TrimSpace(str("security"))); v != "" {
			if v != "ssl" && v != "starttls" {
				return "", fmt.Errorf("security must be ssl or starttls (got %q)", v)
			}
			sc.Security = v
		}
		if v, ok := args["insecure"].(bool); ok {
			sc.Insecure = v
		}
		if v := num("list_limit"); v != 0 {
			if v < 1 || v > 500 {
				return "", fmt.Errorf("list_limit must be between 1 and 500 (got %d)", v)
			}
			sc.ListLimit = v
		}
		if err := e.saveEmailConfig(ctx, sc, str("password")); err != nil {
			return "", err
		}
		sec := sc.Security
		if sec == "" {
			sec = "ssl"
		}
		if sc.Insecure {
			sec += ", self-signed accepted"
		}
		return fmt.Sprintf("Email configured (imap=%s smtp=%s user=%s security=%s). Password %s.",
			sc.IMAPHost, sc.SMTPHost, sc.User, sec,
			map[bool]string{true: "stored", false: "unchanged"}[str("password") != ""]), nil

	case "list", "inbox", "":
		cfg, err := e.loadEmailConfig(ctx)
		cfg.Folder = str("folder")
		cfg.Offset = num("offset")
		if err != nil {
			return "", err
		}
		limit := num("limit")
		msgs, err := cfg.List(limit)
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			return "Folder is empty.", nil
		}
		return jsonResult(msgs), nil

	case "read", "open":
		cfg, err := e.loadEmailConfig(ctx)
		cfg.Folder = str("folder")
		cfg.Offset = num("offset")
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
		return emailReadResult(msg, num("offset"))

	case "search":
		cfg, err := e.loadEmailConfig(ctx)
		cfg.Folder = str("folder")
		cfg.Offset = num("offset")
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
		cfg.Folder = str("folder")
		cfg.Offset = num("offset")
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
		cfg.Folder = str("folder")
		cfg.Offset = num("offset")
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

	case "rules", "rule_save", "rule_delete", "rule_preview", "rules_apply":
		action := str("action")
		if action == "rules" {
			rules, err := email.LoadRules(ctx, e.userStore())
			return jsonResult(rules), err
		}
		if action == "rule_delete" {
			return "Rule deleted.", email.DeleteRule(ctx, e.userStore(), str("name"))
		}
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		rules, err := email.LoadRules(ctx, e.userStore())
		if err != nil {
			return "", err
		}
		if action == "rule_save" || (action == "rule_preview" && str("rule_field") != "") {
			rule := email.Rule{Name: str("name"), Field: str("rule_field"), Contains: str("rule_contains"), Action: str("rule_action"), Target: str("target"), Enabled: true}
			// A partial save modifies only the fields supplied by the caller.
			for _, r := range rules {
				if r.Name == rule.Name {
					rule = r
					break
				}
			}
			if v, ok := args["enabled"].(bool); ok {
				rule.Enabled = v
			}
			for key, dst := range map[string]*string{"rule_field": &rule.Field, "rule_contains": &rule.Contains, "rule_action": &rule.Action, "target": &rule.Target} {
				if v, ok := args[key].(string); ok {
					*dst = v
				}
			}
			if err = rule.Validate(); err != nil {
				return "", err
			}
			if rule.Action == "move" {
				fs, err := cfg.Folders()
				if err != nil {
					return "", err
				}
				found := false
				for _, f := range fs {
					if f.Name == rule.Target && f.Selectable {
						found = true
					}
				}
				if !found {
					return "", fmt.Errorf("target folder not found; use folders or create_folder first")
				}
			}
			if action == "rule_save" {
				return "Rule saved. Enabled rules run every minute on INBOX, including existing matching mail, while Prism is running.", email.SaveRule(ctx, e.userStore(), rule)
			}
			rule.Enabled = true
			rules = []email.Rule{rule}
		} else if str("name") != "" {
			selected := []email.Rule{}
			for _, r := range rules {
				if r.Name == str("name") {
					r.Enabled = true
					selected = append(selected, r)
				}
			}
			if len(selected) == 0 {
				return "", fmt.Errorf("rule not found; use rules")
			}
			rules = selected
		}
		result, err := cfg.RunRules(ctx, rules, action == "rule_preview")
		if action == "rules_apply" && e.memStore != nil {
			for _, r := range result {
				if r.Applied > 0 || r.Error != "" {
					e.memStore.AddUsage(ctx, 0, e.sessionID, "mail_rule", r.Name, int64(r.Applied), map[string]any{"applied": r.Applied, "error": r.Error})
				}
			}
		}
		return jsonResult(result), err

	case "folders":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		fs, err := cfg.Folders()
		return jsonResult(fs), err
	case "create_folder", "rename_folder", "delete_folder":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		resolved, err := cfg.ManageFolderWithRules(ctx, e.userStore(), str("action"), str("folder"), str("target"))
		if err != nil {
			return "", err
		}
		return jsonResult(map[string]string{"folder": resolved, "status": "updated"}), nil
	case "move", "archive", "trash", "mark_read", "mark_unread":
		cfg, err := e.loadEmailConfig(ctx)
		if err != nil {
			return "", err
		}
		cfg.Folder = str("folder")
		uid := uint32(num("uid"))
		if uid == 0 {
			return "", fmt.Errorf("uid from list or search is required")
		}
		action := str("action")
		if action == "mark_read" || action == "mark_unread" {
			return "Message flag updated.", cfg.SetSeen(uid, action == "mark_read")
		}
		target := str("target")
		if action == "archive" || action == "trash" {
			target, err = cfg.SpecialFolder(action)
			if err != nil {
				return "", err
			}
		}
		if err = cfg.Move(uid, target); err != nil {
			return "", err
		}
		return "Message moved to " + target + ". Its UID may have changed; list that folder to find it.", nil

	default:
		return "", fmt.Errorf("email: unknown action %q (config, list, read, search, send, reply)", str("action"))
	}
}
