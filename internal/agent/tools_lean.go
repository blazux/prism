package agent

import "prism/internal/ollama"

// Curated descriptions, never blind truncation: parameter schemas and provider
// tools remain intact. Only native definitions are copied, before dynamic tools
// are appended, so a custom/MCP tool cannot acquire a native tool's description.
var leanToolDescriptions = map[string]string{
	"agent_settings": "Read/change agent behavior or AI sources. ai_get lists sources and capabilities; ai_source_* manages one source; ai_set edits the default without removing others. AI config: Settings in single-user, global admin in multi-user; shared-agent behavior needs group admin. Credentials: request_secret, then pass key_secret NAME, never a key. Tests may incur charges. Embedding changes apply automatically; reindex=true requires user agreement to rebuild and send indexed text to the provider. Check progress with ai_get; ai_reset only if serverDefaultsAvailable. Changes apply next message.",
	"cron":           "Schedule workspace shell jobs, also visible read-only in Tasks. add requires name, schedule, command, description. update edits in place; disable pauses. PRISM_URL/PRISM_SESSION/PRISM_TOKEN are injected; other secrets are not: fetch /api/user/secrets/<name>?session=$PRISM_SESSION with Bearer $PRISM_TOKEN (JSON value). For scheduled agent answers POST $PRISM_URL/api/chat with Bearer auth and JSON {message,session:$PRISM_SESSION,deliver:telegram|slack|webex}; never hardcode a channel as session. For exact text use /api/telegram/send or /api/webex/send with {text}; Webex targets the group's announcement room.",
	"widget":         "HTML/JS iframe panels for requested widgets or persistent views; answer ordinary questions in chat. add needs title+content; update changes only supplied fields. Before update/remove, list for exact id; removal requires user request. add/update return screenshot+console errors: inspect once, fix defects, stop when correct. list_shared browses the group gallery; add_shared copies to this board; share publishes this widget or kind=dashboard; unshare removes the gallery entry, not existing copies (author/group admin only).",
	"note":           "Persistent Markdown notes. Prefer editor for the open note, including unsaved text. List for exact id before update/delete; delete only the requested note. Offer to save substantial outputs. Images: save under data/ and embed ![alt](/data/path); /api/file serves text, not images. Omitted fields stay unchanged; empty body clears.",
	"prism_help":     "Read Prism documentation and current user configuration before advising on Prism setup/capabilities. Omit topic for index+configuration; use a returned topic for full instructions. Guide from the user's actual setup.",
	"skill":          "Save reusable procedures, including those taught by the user. Names/when-to-use are always indexed in the prompt; get loads full steps. Use save_learning for one-off gotchas instead.",
	"save_learning":  "Store a one-off lesson/gotcha after a confirmed solution. Retrieval is similarity-based (top 3, thresholded), not guaranteed. Store reusable procedures with skill; its index is always present.",
	"deep_research":  "Multi-round web search, reading and model synthesis; returns a sourced Markdown report. Slow and potentially costly. For broad research only; use web_search for a quick lookup.",
	"editor":         "Read/update the open note, mail draft, task or calendar form, including unsaved text. Read first; update requested fields with the returned revision. Stale revision: reread and reconsider. Notes save immediately; mail/tasks/events stay in the form until user saves. Never sends mail. Use note for other stored notes; email to send only when requested.",
	"pim_source":     "Configure calendar/tasks/notes sources. CalDAV and Todoist connections are tested before saving; credentials are stored secret NAMES from request_secret. Google/Microsoft require browser OAuth in Settings → Calendar. Notes vault path is an absolute server-container directory. status shows current sources.",
	"channel":        "Manage user messaging. telegram_connect takes a stored BotFather token_secret NAME; validated before saving, then user sends /start to link. Slack requires global admin in Settings → Channels; Webex requires group admin in the admin console.",
	"webhook":        "Inbound POST becomes an agent message. add returns URL and X-Prism-Token; prompt can contain {{content}}. Default: isolated webhook session. deliver forwards answer to a channel; respond returns it synchronously. update preserves URL/token; remove uses id from list.",
	"browser_act":    "Browser actions with persistent session cookies. Captures console/errors automatically. Use for interaction or investigating a defective preview; a clean widget auto-preview needs no second screenshot.",
	"browser_get":    "Read a page with Chromium (JS/SPAs/local HTML), optionally evaluate a JS expression. For static pages/APIs use http_request.",
	"rag_ingest":     "Index inline content or one workspace file into a collection. source_path parses PDF, converts PPTX, otherwise reads text. No directories: list then ingest files individually. For agent gotchas use save_learning.",
	"rag_manage":     "list collections, or documents when collection is set. delete removes document if provided, otherwise the entire collection. describe sets the collection summary shown in Settings and prompt; describe new collections.",
	"request_secret": "Secure user dialog for credentials; stored encrypted, never in chat. Existing names return immediately. exec_command receives uppercased env vars; cron must fetch via the scoped secrets API.",
	"secrets":        "list names (including group secrets), never values. delete personal secret; share MOVES a personal secret to a group without exposing its value. Create with request_secret.",
	"docker_run":     "Start a service with auto-allocated port. Use returned widget/script URLs for the configured backend. Prefer a Docker image over installing a service into the workspace.",
	"docker_manage":  "ps: Prism services; list: visible containers; logs/exec: service inspection; stop: stop AND remove a docker_run service.",
	"docker_compose": "Manage multi-service stacks from a workspace Compose file (write_file/wget first). Names use prism-svc-<dir>-<service>-1. down stops and removes the stack.",
	"search_history": "Search previous conversations across workspaces, global assistant and Telegram. Returns dated messages with session IDs; use when a past fact is missing from current context.",
}

func nativeToolsFor(lean bool) []ollama.Tool {
	tools := append([]ollama.Tool(nil), ToolDefinitions...)
	if lean {
		for i := range tools {
			if description, ok := leanToolDescriptions[tools[i].Function.Name]; ok {
				tools[i].Function.Description = description
			}
		}
	}
	return tools
}
