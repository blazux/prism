package agent

import (
	"maps"
	"strings"

	"prism/internal/ollama"
)

// Filter schemas without changing the global definitions or weakening execution
// guards. Composite tools retain only actions that this caller can actually use.
func (e *ToolExecutor) availableTool(tool ollama.Tool) (ollama.Tool, bool) {
	name := tool.Function.Name
	if e.hiddenTools[name] {
		return tool, false
	}
	// Preserve the established full-access browser contracts. Filtering is useful
	// for restricted callers and headless channels; the admin browser benchmark
	// showed extra verification calls after pruning its otherwise unrestricted menu.
	if e.globalAdmin && !e.headless {
		return tool, true
	}
	for _, serverTool := range []string{"webhook", "pim_source", "channel", "subagent"} {
		if name == serverTool && e.serverTools[name] == nil {
			return tool, false
		}
	}
	if name == "editor" && e.editorToolFn == nil {
		return tool, false
	}
	if e.headless && (name == "editor" || name == "request_secret") {
		return tool, false
	}
	if name == "agent_settings" && (strings.HasPrefix(e.sessionID, "room-") || strings.HasPrefix(e.sessionID, "webex-") || strings.HasPrefix(e.sessionID, "teams-")) {
		return tool, false
	}
	action, composite := tool.Function.Parameters.Properties["action"]
	if !composite || len(action.Enum) == 0 {
		return tool, e.toolGuard == nil || e.toolGuard(name, nil) == nil
	}
	var allowed []string
	for _, value := range action.Enum {
		if e.toolGuard != nil && e.toolGuard(name, map[string]interface{}{"action": value}) != nil {
			continue
		}
		if e.headless && name == "widget" && (value == "add" || value == "update" || value == "add_shared") {
			continue
		}
		if name == "widget" && len(e.sharingGroups) == 0 && (value == "share" || value == "unshare" || value == "list_shared" || value == "add_shared") {
			continue
		}
		if name == "agent_settings" && e.multiUser && !e.globalAdmin && strings.HasPrefix(value, "ai_") {
			continue
		}
		allowed = append(allowed, value)
	}
	if len(allowed) == 0 {
		return tool, false
	}
	tool.Function.Parameters.Properties = maps.Clone(tool.Function.Parameters.Properties)
	action.Enum = allowed
	tool.Function.Parameters.Properties["action"] = action
	if name == "widget" {
		if len(e.sharingGroups) == 0 {
			for _, key := range []string{"kind", "group", "description"} {
				delete(tool.Function.Parameters.Properties, key)
			}
			for _, section := range []string{"Group sharing (multi-user):", "list_shared browses", "list_shared/add_shared"} {
				tool.Function.Description = strings.TrimSpace(strings.Split(tool.Function.Description, section)[0])
			}
			id := tool.Function.Parameters.Properties["id"]
			id.Description = "Exact widget ID from list; optional when adding a widget."
			tool.Function.Parameters.Properties["id"] = id
		}
		if e.headless {
			for _, key := range []string{"title", "content", "cols", "height", "resources"} {
				delete(tool.Function.Parameters.Properties, key)
			}
			tool.Function.Description = "Manage existing widgets. Available actions: " + strings.Join(allowed, ", ") + ". Use list to get exact widget IDs; remove only on explicit user request. Sharing uses numeric IDs from list_shared for unshare."
		}
	}
	if name == "agent_settings" && e.multiUser && !e.globalAdmin {
		keep := map[string]bool{"action": true, "name": true, "max_iterations": true, "thinking": true, "prompt_profile": true, "personality": true, "timezone": true, "reasoning_effort": true}
		for key := range tool.Function.Parameters.Properties {
			if !keep[key] {
				delete(tool.Function.Parameters.Properties, key)
			}
		}
		tool.Function.Description = "Read or change your agent's name, personality, timezone, turn budget, thinking and prompt profile. action=get or set. Changes apply next message."
	}
	return tool, true
}
