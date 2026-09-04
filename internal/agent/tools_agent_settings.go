package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"prism/internal/memory"
)

// agentSettings reads/writes the caller's Settings › Agent values (name, turn
// budget, extended reasoning, lean prompt, reasoning effort) — the same keys
// handleAgentLimits/handleAgentName write, re-read by loadProfile on the next
// turn. A group's shared agent has its own copy in room_config, edited by a
// group admin in the admin console, so it is refused here rather than silently
// writing to the wrong place.
func (e *ToolExecutor) agentSettings(ctx context.Context, action string, args map[string]any) (string, error) {
	if strings.HasPrefix(e.sessionID, "room-") || strings.HasPrefix(e.sessionID, "webex-") {
		return "This is a group's shared agent: its name, turn budget, reasoning and prompt profile are set by a group admin in the admin console → Shared agent, not here.", nil
	}
	if action != "get" && action != "" && action != "set" {
		return "", fmt.Errorf("agent_settings: unknown action %q (expected get, set)", action)
	}
	us := e.userStore()
	if us == nil {
		return "Settings store not available (Postgres not configured).", nil
	}
	get := func(k string) string { v, _, _ := us.GetConfig(ctx, k); return strings.TrimSpace(v) }
	render := func() string {
		name, iter, effort := get(memory.KeyAgentName), get(memory.KeyAgentMaxIterations), get(memory.KeyAgentReasoningEffort)
		if name == "" {
			name = "(default)"
		}
		if iter == "" {
			iter = fmt.Sprintf("%d (default)", DefaultMaxIterations)
		}
		if effort == "" {
			effort = "server default"
		}
		return fmt.Sprintf("Agent settings (Settings → Agent):\n- name: %s\n- max_iterations: %s\n- thinking (extended reasoning): %v\n- lean_prompt: %v\n- reasoning_effort: %s\nChanges take effect from the next message.",
			name, iter, get(memory.KeyAgentThinking) != "off", get(memory.KeyAgentLeanPrompt) == "on", effort)
	}
	if action != "set" {
		return render(), nil
	}
	var changed []string
	set := func(label, key, value string) error {
		if err := us.SetConfig(ctx, key, value); err != nil {
			return fmt.Errorf("saving %s: %w", label, err)
		}
		changed = append(changed, label)
		return nil
	}
	if v, ok := args["name"].(string); ok {
		if err := set("name", memory.KeyAgentName, strings.TrimSpace(v)); err != nil {
			return "", err
		}
	}
	if v, ok := args["max_iterations"].(float64); ok {
		mv := ""
		if n := ClampIterations(int(v)); n > 0 {
			mv = strconv.Itoa(n)
		}
		if err := set("max_iterations", memory.KeyAgentMaxIterations, mv); err != nil {
			return "", err
		}
	}
	if v, ok := args["thinking"].(bool); ok {
		tv := "on"
		if !v {
			tv = "off"
		}
		if err := set("thinking", memory.KeyAgentThinking, tv); err != nil {
			return "", err
		}
	}
	if v, ok := args["lean_prompt"].(bool); ok {
		lv := "off"
		if v {
			lv = "on"
		}
		if err := set("lean_prompt", memory.KeyAgentLeanPrompt, lv); err != nil {
			return "", err
		}
	}
	if v, ok := args["reasoning_effort"].(string); ok {
		ev := NormalizeReasoningEffort(v)
		if t := strings.ToLower(strings.TrimSpace(v)); t != "" && t != "default" && ev == "" {
			return fmt.Sprintf("reasoning_effort %q is not one of %s (or 'default' for the server default) — nothing changed.", v, strings.Join(ReasoningEfforts, ", ")), nil
		}
		if err := set("reasoning_effort", memory.KeyAgentReasoningEffort, ev); err != nil {
			return "", err
		}
	}
	if len(changed) == 0 {
		return "Nothing to change: pass at least one of name, max_iterations, thinking, lean_prompt, reasoning_effort.", nil
	}
	return "Updated " + strings.Join(changed, ", ") + ".\n" + render(), nil
}
