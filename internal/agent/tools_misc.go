package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"prism/internal/mcp"
)

func (e *ToolExecutor) scheduleNotification(title, message, level string, delaySeconds int) (string, error) {
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	if delaySeconds <= 0 {
		return "", fmt.Errorf("delay_seconds must be positive")
	}
	if delaySeconds > 86400 {
		return "", fmt.Errorf("delay_seconds must be <= 86400 (24h)")
	}
	if level == "" {
		level = "info"
	}
	cb := e.onNotification
	delay := time.Duration(delaySeconds) * time.Second
	when := time.Now().Add(delay).Format("15:04:05")
	if cb != nil {
		time.AfterFunc(delay, func() { cb(title, message, level) })
	}
	return fmt.Sprintf("Notification scheduled in %ds (at ~%s): [%s] %s", delaySeconds, when, level, title), nil
}

func (e *ToolExecutor) sendNotification(title, message, level string) (string, error) {
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	if level == "" {
		level = "info"
	}
	if e.onNotification != nil {
		e.onNotification(title, message, level)
	}
	return fmt.Sprintf("Notification sent: [%s] %s", level, title), nil
}

// ─── Secret tools ─────────────────────────────────────────────────────────────

// isReservedSecretName reports whether a (scope-prefix-stripped) secret name
// belongs to a built-in integration (email, CalDAV, Telegram, Slack, Webex,
// MCP OAuth) rather than a key request_secret created for a script to use.
// Both live in the same per-user/per-group scope, so name is the only way to
// tell them apart — these must never be handed to arbitrary script execution.
func isReservedSecretName(name string) bool {
	switch name {
	case "email_password", "caldav_password", "todoist_token",
		"telegram_bot_token", "slack_bot_token", "slack_app_token":
		return true
	}
	return strings.HasPrefix(name, "webex_bot_token:") || strings.HasPrefix(name, "mcp_oauth_")
}

func toEnvVarName(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return strings.Trim(sb.String(), "_")
}

func (e *ToolExecutor) requestSecret(ctx context.Context, name, description string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	// Normalize name
	name = strings.ToLower(strings.TrimSpace(name))
	envVar := toEnvVarName(name)

	if us := e.userStore(); us != nil {
		if _, exists, _ := us.GetSecret(ctx, name); exists {
			return fmt.Sprintf("Secret '%s' already stored. Available as os.environ['%s'] / $%s. Use delete_secret to replace it.", name, envVar, envVar), nil
		}
	}

	if e.onSecretRequest == nil {
		return "", fmt.Errorf("secret request not available in this context")
	}
	if err := e.onSecretRequest(ctx, name, description); err != nil {
		return "", err
	}
	return fmt.Sprintf("Secret '%s' stored securely. Use os.environ['%s'] in Python or $%s in shell scripts.", name, envVar, envVar), nil
}

func (e *ToolExecutor) listSecrets(ctx context.Context) (string, error) {
	us := e.userStore()
	if us == nil {
		return "Secret store not available (Postgres not configured).", nil
	}
	// personalScope() is "" for single-user/legacy sessions (no group, no
	// user identity) — ListScopedSecretNames deliberately returns nothing for
	// an empty scope (it's meant for already-scoped u<id>/g<id> stores only),
	// so fall back to the old unscoped listing there to keep single-user mode
	// byte-for-byte unchanged.
	var names []string
	var err error
	if e.SecretsScope() == "" {
		names, err = e.memStore.ListSecretNames(ctx)
	} else {
		names, err = us.ListScopedSecretNames(ctx)
	}
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(names) == 0 {
		return "No secrets stored. Use request_secret to store one.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Stored secrets (%d):\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&sb, "  - %s  →  env var: %s\n", n, toEnvVarName(n))
	}
	return sb.String(), nil
}

func (e *ToolExecutor) deleteSecret(ctx context.Context, name string) (string, error) {
	us := e.userStore()
	if us == nil {
		return "Secret store not available (Postgres not configured).", nil
	}
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if err := us.DeleteSecret(ctx, name); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	return fmt.Sprintf("Secret '%s' deleted.", name), nil
}

// ─── MCP tools ────────────────────────────────────────────────────────────────

// mcpPersonalBlockedMsg is returned when MULTI_USER retires personal MCP
// management via the agent's own `mcp` tool — group MCP stays reachable
// through the same tool's list action and through Admin console management.
const mcpPersonalBlockedMsg = "Personal MCP servers are not available in multi-user mode. Ask your group admin to add the server for your group from the Admin console."

func (e *ToolExecutor) mcpAddServer(ctx context.Context, name, url, authSecret string) (string, error) {
	if e.multiUser {
		return mcpPersonalBlockedMsg, nil
	}
	if e.mcpMgr == nil {
		return "MCP not available (Postgres required)", nil
	}
	if name == "" || url == "" {
		return "", fmt.Errorf("name and url are required")
	}
	tools, err := e.mcpMgr.Connect(ctx, e.mcpStorageScope(), name, url, authSecret)
	if err != nil {
		return fmt.Sprintf("Failed to connect MCP server '%s': %v", name, err), nil
	}
	if e.onMCPReload != nil {
		e.onMCPReload()
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return fmt.Sprintf("MCP server '%s' connected — %d tools available: %s",
		name, len(tools), strings.Join(names, ", ")), nil
}

func (e *ToolExecutor) mcpRemoveServer(ctx context.Context, name string) (string, error) {
	if e.multiUser {
		return mcpPersonalBlockedMsg, nil
	}
	if e.mcpMgr == nil {
		return "MCP not available", nil
	}
	if err := e.mcpMgr.Remove(ctx, e.mcpStorageScope(), name); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if e.onMCPReload != nil {
		e.onMCPReload()
	}
	return fmt.Sprintf("MCP server '%s' removed.", name), nil
}

func (e *ToolExecutor) mcpListServers(ctx context.Context) (string, error) {
	if e.mcpMgr == nil {
		return "MCP not available (Postgres required).", nil
	}
	// Personal servers (this user, any board) plus the group's shared servers
	// (if any) — the same two layers AllDynamicTools exposes as callable tools,
	// so this list matches what the agent can actually reach. MULTI_USER
	// retires the personal tier entirely: group scope only.
	var servers []mcp.ServerConfig
	if !e.multiUser {
		var err error
		servers, err = e.mcpMgr.List(ctx, e.mcpStorageScope())
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
	}
	if scope := e.ragScope; scope != "" && (e.multiUser || scope != e.personalScope()) {
		if groupServers, err := e.mcpMgr.List(ctx, scope); err == nil {
			seen := make(map[string]bool, len(servers))
			for _, s := range servers {
				seen[s.Name] = true
			}
			for _, s := range groupServers {
				if !seen[s.Name] {
					servers = append(servers, s)
				}
			}
		}
	}
	if len(servers) == 0 {
		return "No MCP servers configured. Use mcp action=add to connect one.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "MCP servers (%d):\n", len(servers))
	for _, srv := range servers {
		status := "enabled"
		if !srv.Enabled {
			status = "disabled"
		}
		toolNames := make([]string, len(srv.Tools))
		for i, t := range srv.Tools {
			toolNames[i] = t.Name
		}
		fmt.Fprintf(&sb, "  • %s [%s] — %d tools: %s\n    URL: %s\n",
			srv.Name, status, len(srv.Tools), strings.Join(toolNames, ", "), srv.URL)
	}
	return sb.String(), nil
}

// ─── HTML extraction ──────────────────────────────────────────────────────────
