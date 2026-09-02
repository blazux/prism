package agent

import (
	"context"
	"fmt"
	"regexp"
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

// IsReservedSecretName reports whether a (scope-prefix-stripped) secret name
// belongs to a built-in integration (email, CalDAV, Telegram, Slack, Webex,
// MCP OAuth) rather than a key request_secret created for a script to use.
// Both live in the same per-user/per-group scope, so name is the only way to
// tell them apart — these must never be handed to arbitrary script execution.
// Exported: internal/server's secrets handlers apply the same rule to decide
// which group secrets a non-admin member may create or delete.
func IsReservedSecretName(name string) bool {
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

// groupScopeRe matches a group tenant scope ("g<id>") — and only that: "global"
// or a board id starting with g must not be mistaken for a group's secrets.
var groupScopeRe = regexp.MustCompile(`^g\d+$`)

// groupSecretsScope returns the group scope whose shared secrets this session
// also receives, beyond its own SecretsScope: group secrets are shared by
// design, so a member's personal session (SecretsScope "u<id>", ragScope
// "g<id>") gets the group tier too. Empty when the session already runs under
// the group scope (room-g<id> boards) or has no group.
func (e *ToolExecutor) groupSecretsScope() string {
	if g := e.ragScope; groupScopeRe.MatchString(g) && g != e.SecretsScope() {
		return g
	}
	return ""
}

// secretsEnv returns the env entries for this session's usable secrets: the
// group's shared tier first (when there is one), overlaid by the session's own
// scope so a personal secret with the same name wins. Reserved integration
// credentials (email, OAuth, …) never leak into script execution.
func (e *ToolExecutor) secretsEnv(ctx context.Context) map[string]string {
	env := map[string]string{}
	if e.memStore == nil {
		return env
	}
	scopes := []string{e.SecretsScope()}
	if g := e.groupSecretsScope(); g != "" {
		scopes = []string{g, e.SecretsScope()}
	}
	for _, scope := range scopes {
		secrets, err := e.memStore.ScopedSecrets(ctx, scope)
		if err != nil {
			continue
		}
		for name, value := range secrets {
			if IsReservedSecretName(name) {
				continue
			}
			env[toEnvVarName(name)] = value
		}
	}
	return env
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
	// The group's shared tier counts as "already stored" too — don't make the
	// user re-enter a credential the whole group already shares.
	if g := e.groupSecretsScope(); g != "" && e.memStore != nil {
		if _, exists, _ := e.memStore.ConfigScope(g).GetSecret(ctx, name); exists && !IsReservedSecretName(name) {
			return fmt.Sprintf("Secret '%s' is already shared by your group. Available as os.environ['%s'] / $%s.", name, envVar, envVar), nil
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
	// Group secrets are shared with every member: list them alongside the
	// personal tier (reserved integration credentials stay hidden — they are
	// never exposed to scripts, so listing them would only mislead).
	var groupNames []string
	if g := e.groupSecretsScope(); g != "" {
		if gn, gerr := e.memStore.ConfigScope(g).ListScopedSecretNames(ctx); gerr == nil {
			for _, n := range gn {
				if !IsReservedSecretName(n) {
					groupNames = append(groupNames, n)
				}
			}
		}
	}
	if len(names) == 0 && len(groupNames) == 0 {
		return "No secrets stored. Use request_secret to store one.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Stored secrets (%d):\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&sb, "  - %s  →  env var: %s\n", n, toEnvVarName(n))
	}
	if len(groupNames) > 0 {
		fmt.Fprintf(&sb, "Group secrets, shared with all members (%d):\n", len(groupNames))
		for _, n := range groupNames {
			fmt.Fprintf(&sb, "  - %s  →  env var: %s\n", n, toEnvVarName(n))
		}
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
	// auth_secret is sent verbatim as a Bearer token to `url`. A reserved name
	// (email_password, telegram_bot_token, an mcp_oauth_* / webex token…) is an
	// integration credential — handing it to an arbitrary MCP endpoint would
	// exfiltrate it (agent-review finding #4). Only secrets the agent created
	// for this purpose via request_secret may be used.
	if authSecret != "" && IsReservedSecretName(authSecret) {
		return fmt.Sprintf("Refused: %q is a reserved integration credential and cannot be sent to an MCP server. "+
			"If this server needs a token, create a dedicated one with request_secret and pass that name as auth_secret.", authSecret), nil
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
