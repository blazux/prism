package agent

import "prism/internal/ollama"

// Minimal keeps every native capability and parameter schema. Only human-facing
// prose is shorter; complex workflows remain available through prism_help.
var minimalToolDescriptions = map[string]string{
	"agent_settings": "Read/change agent behavior and AI sources. ai_get lists sources; ai_source_* manage one; ai_set edits the default. AI settings are admin-only in multi-user; group behavior needs a group admin. Use stored key_secret names from request_secret, never raw keys. ai_test may cost tokens. reindex needs user consent. Changes apply next message.",
	"email":          "Mail: list/search/read/send/reply, folders, moves, flags and rules. Open drafts use editor (it never sends). Keep folder with UID; if read is truncated, continue at next_offset. Rules can run automatically on INBOX; preview before applying. Use prism_help topic=email for detailed workflows.",
	"cron":           "Schedule workspace commands: list/add/update/enable/disable/remove. add needs name, schedule, command and description. Jobs get PRISM_URL/PRISM_SESSION/PRISM_TOKEN, but not secret values; fetch secrets via /api/user/secrets/<name>?session=$PRISM_SESSION with Bearer $PRISM_TOKEN. To schedule an agent answer, POST /api/chat with {message,session:$PRISM_SESSION,deliver:telegram|slack|webex}; exact text uses /api/telegram/send or /api/webex/send. Never hardcode a channel as session.",
	"widget":         "HTML/JS dashboard panels for requested persistent views; answer one-off questions in chat. add needs title+content; update changes supplied fields. List for exact id before update/remove; remove only on request. add/update return screenshot+console errors: inspect once, fix defects. list_shared/add_shared/share/unshare manage the group gallery; unshare keeps existing copies.",
	"editor":         "Read/update the open note, mail draft, task or calendar form, including unsaved text. Read first; update with returned revision. A stale revision requires rereading. Notes save immediately; other forms await user save. Never sends mail.",
	"pim_source":     "Configure calendar, tasks and notes sources; status shows connections. CalDAV/Todoist secrets are stored names from request_secret and connections are tested. Google/Microsoft require browser OAuth in Settings. Notes vault path is absolute inside the server container.",
	"webhook":        "Manage inbound POST hooks (list/add/update/remove). add returns URL and X-Prism-Token; prompt can use {{content}}. Default is an isolated session; deliver forwards the answer, respond returns it synchronously. update preserves URL/token; remove by listed id.",
	"note":           "Persistent Markdown notes. For the open note use editor to include unsaved text. List for exact id before update/delete; delete only on request. Omitted fields stay unchanged; empty body clears. Images: save under data/ and embed ![alt](/data/path); /api/file serves text, not images.",
}

var minimalToolPropertyDescriptions = map[string]map[string]string{
	"agent_settings": {
		"embedding_source_id":  "Existing source ID; empty selects a separate connection.",
		"base_url":             "Provider URL; custom servers require admin.",
		"embedding_base_url":   "Embedding URL; OpenAI-compatible URLs include /v1.",
		"embedding_model":      "Model ID; empty disables document search.",
		"embedding_key_secret": "Stored secret name for separate embeddings.",
		"reindex":              "Requires user consent: rebuilds index and sends indexed text to provider.",
		"key_secret":           "Stored secret name, never the raw API key.",
		"name":                 "Agent display name; empty restores default.",
		"max_iterations":       "Model calls per turn (10–500; 0=default 75).",
		"prompt_profile":       "guided/standard/minimal; applies next message.",
		"personality":          "Default in all workspaces; empty clears. One workspace: update_system_prompt.",
		"timezone":             "Personal IANA timezone; empty resets default. Existing cron jobs keep theirs.",
		"reasoning_effort":     "Reasoning level; available values depend on the model.",
	},
	"email": {
		"folder":          "Exact folder name; default INBOX. UIDs are folder-scoped.",
		"target":          "Destination folder; new name for rename_folder.",
		"offset":          "list: skipped messages; read: character offset from next_offset.",
		"name":            "Rule name; rule_save updates it, preserving omitted fields.",
		"enabled":         "rule_save: run every minute if true; false pauses.",
		"to":              "Recipient for send; reply defaults to original sender.",
		"password":        "Legacy raw password; prefer password_secret.",
		"password_secret": "Stored secret name from request_secret.",
		"security":        "ssl=implicit TLS; starttls=port upgrade.",
		"list_limit":      "Inbox page size (1–500; default 60).",
	},
	"cron": {
		"action":      "Job operation; disable pauses, update edits in place.",
		"schedule":    "Cron expression, e.g. */30 * * * *.",
		"command":     "Workspace shell command to run.",
		"description": "Required on add: what the job does and which view it feeds.",
	},
	"widget": {
		"id":      "Exact widget id from list; numeric id from list_shared for add_shared/unshare.",
		"content": "Complete self-contained HTML with CSS/JS as needed.",
		"cols":    "Width: 1 small, 2 medium, 3 full.",
	},
	"editor": {
		"body":  "Replacement body; read all pages first. Empty clears.",
		"due":   "Task due date/time in editor timezone; empty clears.",
		"start": "Event start; changing it alone preserves duration.",
		"end":   "Event end; all-day end is exclusive.",
	},
	"pim_source": {
		"url":             "CalDAV server URL.",
		"password_secret": "Stored CalDAV password secret name from request_secret.",
		"token_secret":    "Stored Todoist token secret name from request_secret.",
		"path":            "Absolute notes vault path inside the server container.",
	},
}

// minimalToolCatalog never mutates the shared definitions or the Standard
// profile. Maps are copied before changing any property description.
func minimalToolCatalog(in []ollama.Tool) []ollama.Tool {
	out := append([]ollama.Tool(nil), in...)
	for i := range out {
		name := out[i].Function.Name
		if description, ok := minimalToolDescriptions[name]; ok {
			out[i].Function.Description = description
		}
		overrides := minimalToolPropertyDescriptions[name]
		if len(overrides) == 0 {
			continue
		}
		old := out[i].Function.Parameters.Properties
		props := make(map[string]ollama.ToolProperty, len(old))
		for key, property := range old {
			if description, ok := overrides[key]; ok {
				property.Description = description
			}
			props[key] = property
		}
		out[i].Function.Parameters.Properties = props
	}
	return out
}
