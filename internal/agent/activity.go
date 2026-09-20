package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Only curated, non-secret identifiers go into the human activity feed.
// Never persist tool bodies, credentials, shell commands or arbitrary output.
func activityDetails(name string, raw json.RawMessage) (map[string]any, bool) {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	str := func(k string) string { v, _ := args[k].(string); return v }
	action := str("action")
	label := name
	if action != "" {
		label += " · " + action
	}
	meta := map[string]any{"label": label, "action": action}
	meaningful := false
	switch canonicalToolName(name) {
	case "task", "calendar", "note", "widget", "cron", "email", "webhook", "editor":
		switch action {
		case "add", "create", "update", "edit", "delete", "remove", "done", "complete", "reopen", "send", "reply", "move", "archive", "trash", "mark_read", "mark_unread", "create_folder", "rename_folder", "delete_folder", "rule_save", "rule_delete", "share", "unshare", "enable", "disable":
			meaningful = true
		}
	case "write_file", "edit", "delete_file", "register_tool", "docker_run", "place_call", "cancel_call":
		meaningful = true
	}
	// Useful context for known resources, without collecting message text or
	// recipient addresses. The full operation remains in its conversation.
	if meaningful {
		for _, key := range []string{"title", "id", "folder", "target", "name", "path"} {
			v := str(key)
			if v == "" {
				continue
			}
			if len(v) > 160 {
				v = v[:160]
				for !utf8.ValidString(v) {
					v = v[:len(v)-1]
				}
			}
			if strings.ContainsAny(v, "\r\n") {
				v = strings.SplitN(v, "\n", 2)[0]
			}
			meta[key] = v
		}
	}
	return meta, meaningful
}
