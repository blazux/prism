package agent

import (
	"prism/internal/ollama"
)

var ToolDefinitions = []ollama.Tool{
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "docker_run",
			Description: "Start a Docker service container. A host port is auto-allocated and http://<name>.localhost/ is configured via Traefik — use that URL for iframes and API calls in widgets (see Docker service networking in the system prompt). Prefer this over apt/pip installs for anything that has a Docker image.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"image":       {Type: "string", Description: "Docker image to run (e.g. 'jgraph/drawio')"},
					"name":        {Type: "string", Description: "Short service name, letters/digits/hyphens only (e.g. 'drawio'). Container will be named prism-svc-<name>."},
					"port":        {Type: "integer", Description: "Primary port the service listens on inside the container (e.g. 3000 for the UI)"},
					"extra_ports": {Type: "array", Description: "Additional internal ports to expose (e.g. [9090, 8080] for metrics or API). Each gets its own auto-allocated host port."},
					"env":         {Type: "object", Description: "Optional environment variables as key/value pairs"},
					"command":     {Type: "string", Description: "Optional command to override the image's default entrypoint (e.g. 'tail -f /dev/null' to keep a CLI image alive, or 'nginx -g daemon off;'). Leave empty to use the image default."},
					"volumes":     {Type: "array", Description: "Ignored — the workspace (/workspace) is automatically available inside every service container via --volumes-from."},
					"gpu":         {Type: "boolean", Description: "Set to true to give the container access to all GPUs (requires nvidia-container-toolkit on the host)"},
					"purpose":     {Type: "string", Description: "One short line: what this service is for and when to use it. Always set it — it's shown back to you in your running-services index so you (later) know what you deployed instead of guessing. E.g. 'Uptime Kuma — status monitoring for the user's self-hosted services'."},
				},
				Required: []string{"image", "name", "port"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "docker_manage",
			Description: "Manage Docker containers. Actions: ps (list Prism service containers), list (all containers on the host), logs (recent logs from a service), exec (run a shell command inside a service container), stop (stop and remove a service started with docker_run).",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":  {Type: "string", Description: "One of: ps, list, logs, exec, stop", Enum: []string{"ps", "list", "logs", "exec", "stop"}},
					"name":    {Type: "string", Description: "Service name as given to docker_run (required for logs, exec, stop)"},
					"command": {Type: "string", Description: "Shell command to run inside the container (exec action only)"},
					"tail":    {Type: "integer", Description: "Number of log lines to return (logs action, default 50)"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "docker_compose",
			Description: "Manage multi-container applications with Docker Compose. Write the docker-compose.yml to workspace first with write_file or wget, then call this tool. Container names are automatically prefixed prism-svc-<dir>-<service>-1.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":  {Type: "string", Description: "Action: up (start all services), down (stop and remove), ps (list services), logs (get logs), restart, exec (run a command inside a service container)", Enum: []string{"up", "down", "ps", "logs", "restart", "exec"}},
					"file":    {Type: "string", Description: "Path to docker-compose.yml relative to /workspace (e.g. 'greenbone/docker-compose.yml')"},
					"project": {Type: "string", Description: "Optional project name override. If omitted, auto-set to prism-svc-<directory> (e.g. 'prism-svc-greenbone')."},
					"service": {Type: "string", Description: "Service name to target for logs, restart, or exec"},
					"command": {Type: "string", Description: "Shell command to run inside the service container (exec action only)"},
					"tail":    {Type: "integer", Description: "Number of log lines per service (default: 50, for logs action)"},
				},
				Required: []string{"action", "file"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "exec_command",
			Description: "Execute a shell command in the workspace container. Use this to run code, compile, test, manage files, etc. Note: the Docker CLI is NOT available here — use docker_run/docker_manage/docker_compose to manage containers.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"command": {Type: "string", Description: "The bash command to execute"},
				},
				Required: []string{"command"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "wget",
			Description: "Download a file from a URL straight to the workspace (streamed to disk — no transcription, no truncation). Always use this instead of http_request + write_file to save a remote file as-is.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"url":  {Type: "string", Description: "HTTP or HTTPS URL to download from"},
					"path": {Type: "string", Description: "Destination path relative to /workspace (e.g. 'greenbone/docker-compose.yml')"},
				},
				Required: []string{"url", "path"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "write_file",
			Description: "Write content to a file in the workspace. Path is relative to /workspace.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"path":    {Type: "string", Description: "File path relative to /workspace (e.g. main.py)"},
					"content": {Type: "string", Description: "File content to write"},
				},
				Required: []string{"path", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "read_file",
			Description: "Read the content of a file in the workspace.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"path": {Type: "string", Description: "File path relative to /workspace"},
				},
				Required: []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "workspace_history",
			Description: "List recent versioned changes to the workspace. Every agent turn is auto-committed to git, so this is the undo history — use it to find a point to roll back to when a file was clobbered or a change went wrong.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"limit": {Type: "integer", Description: "How many recent changes to list (default 20, max 50)."},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "workspace_restore",
			Description: "Roll one file back to how it was at a past commit (a hash from workspace_history) and record the restoration. Restores only the given path, and is itself reversible — nothing is lost.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"commit": {Type: "string", Description: "Commit hash from workspace_history."},
					"path":   {Type: "string", Description: "Workspace-relative path of the file to restore."},
				},
				Required: []string{"commit", "path"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "prism_help",
			Description: "Prism's own user documentation plus what is currently configured for this user. Call it FIRST whenever the user asks how to use or configure Prism, what you can do, where a setting lives, or whether something (email, calendar, notes vault, Telegram, Slack, Webex, MCP, knowledge base) is connected. Without a topic: the list of topics AND the current configuration. With a topic (a name from that list): the full page. Then guide the user step by step from their actual situation.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"topic": {Type: "string", Description: "A topic name from the index (e.g. 'settings-agent', 'connect-telegram'). Omit for the index + current configuration."},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "list_files",
			Description: "List files and directories in the workspace.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"path": {Type: "string", Description: "Directory path relative to /workspace (default: .)"},
				},
				Required: []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "delete_file",
			Description: "Delete a file from the workspace.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"path": {Type: "string", Description: "File path relative to /workspace"},
				},
				Required: []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "install_packages",
			Description: "Install packages in the workspace container with apt-get or pip. Installed packages are recorded and reinstalled automatically on container restart.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"manager":  {Type: "string", Description: "Package manager: apt or pip", Enum: []string{"apt", "pip"}},
					"packages": {Type: "string", Description: "Space-separated list of packages to install"},
				},
				Required: []string{"manager", "packages"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "widget",
			Description: "Manage dashboard widgets — self-contained HTML/JS panels displayed in iframes (see the widget guidelines in the system prompt). Actions: add (title+content required; id optional, auto-derived by slugifying the title if omitted), update (id + any of title/content/cols/height; only provided fields change), remove (id), list. Before update/remove, call list and copy the exact id from its output — never guess an id from memory; remove ONLY when the user explicitly asked for that widget to go. add/update return a screenshot of the rendered widget plus console errors — inspect them and fix any problem before telling the user the widget is ready. Group sharing (multi-user): list_shared (browse widgets/dashboards your group published), add_shared (add one to THIS board — id is the numeric id from list_shared), share (publish a widget of THIS board to your group — id is the widget's id from list; set kind=dashboard to share the whole board; add group=<name> only if you belong to several groups), unshare (remove an item from the group gallery — id is the numeric id from list_shared; only the user's own shares, or anyone's if they admin the group; copies already added to boards are kept).",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":  {Type: "string", Description: "One of: add, update, remove, list, list_shared, add_shared, share, unshare", Enum: []string{"add", "update", "remove", "list", "list_shared", "add_shared", "share", "unshare"}},
					"id":      {Type: "string", Description: "Widget ID (add: optional, derived from title; update/remove/share: the id from list). For add_shared/unshare: the NUMERIC id from list_shared."},
					"kind":    {Type: "string", Description: "For list_shared/share: 'widget' (default) or 'dashboard' (the whole board)."},
					"group":   {Type: "string", Description: "For share: which group to publish to, by name — only needed when you belong to several groups."},
					"title":   {Type: "string", Description: "Widget title shown in the card header"},
					"content": {Type: "string", Description: "Complete self-contained HTML for the widget (include <style> and <script> tags as needed)"},
					"cols":    {Type: "integer", Description: "Width: 1=small (default), 2=medium, 3=full-width"},
					"height":  {Type: "integer", Description: "Height in pixels (default: 280)"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "show_in_editor",
			Description: "Open a file in the user's editor. Does not return the file content — use read_file for that.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"path": {Type: "string", Description: "File path relative to /workspace"},
				},
				Required: []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: "cron",
			Description: "Manage scheduled cron jobs running inside the workspace container. Actions: list, add (name+schedule+command, plus a description), remove (name). Jobs also appear read-only in the user's Tasks list.\n" +
				"To deliver a result to the user on a messaging channel on a schedule, make the command POST a prompt to yourself with delivery enabled. Set \"deliver\" to \"telegram\", \"slack\" or \"webex\" (Webex posts to the announcement room the group admin picked in the Webex config), e.g.:\n" +
				"curl -s -X POST $PRISM_URL/api/chat -H \"Authorization: Bearer $PRISM_TOKEN\" -H 'Content-Type: application/json' -d '{\"message\":\"Summarize my unread emails in 5 bullet points\",\"session\":\"'$PRISM_SESSION'\",\"deliver\":\"telegram\"}'\n" +
				"ALWAYS use $PRISM_SESSION for \"session\" (never a hardcoded channel name like \"telegram\"/\"webex\") — it's the session you're in right now, and it carries THIS chat's knowledge base/MCP servers along to the scheduled run. A hardcoded literal creates a different, empty session with no tools.\n" +
				"Use \"deliver\" only when you want the model's ANSWER delivered. To post an exact text, push a raw line instead:\n" +
				"  Telegram: curl -s -X POST $PRISM_URL/api/telegram/send -H \"Authorization: Bearer $PRISM_TOKEN\" -d '{\"text\":\"Backup finished ✅\"}'\n" +
				"  Webex:    curl -s -X POST $PRISM_URL/api/webex/send -H \"Authorization: Bearer $PRISM_TOKEN\" -d '{\"text\":\"Bonjour à tous !\"}'  (posts to the group's announcement room)\n" +
				"($PRISM_URL/$PRISM_SESSION/$PRISM_TOKEN are available both ways: as real environment variables for a script's own os.environ lookups, and substituted directly into the command text for inline shell $VAR usage.)\n" +
				"SECRETS ARE NOT INJECTED INTO CRON JOBS (unlike exec_command): a scheduled script that reads os.environ['MY_SECRET'] silently fails. Fetch it over HTTP instead: curl -s \"$PRISM_URL/api/user/secrets/<name>?session=$PRISM_SESSION\" -H \"Authorization: Bearer $PRISM_TOKEN\" (JSON with a 'value' field).",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":      {Type: "string", Description: "One of: list, add, remove", Enum: []string{"list", "add", "remove"}},
					"name":        {Type: "string", Description: "Unique job identifier (e.g. 'daily-backup', 'sync-feed')"},
					"schedule":    {Type: "string", Description: "Cron expression (e.g. '*/5 * * * *' every 5 min, '0 9 * * 1-5' weekdays 9am)"},
					"command":     {Type: "string", Description: "Shell command to run (e.g. 'python3 /workspace/myscript.py >> /workspace/logs/myscript.log 2>&1')"},
					"description": {Type: "string", Description: "Required for 'add': one short human sentence explaining what this job does and which widget/dashboard it feeds, so the user can tell at a glance what it's for (e.g. 'Refreshes the weather widget's data every 30 min')."},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "http_request",
			Description: "Make an HTTP request and return the response. For GET requests to HTML pages, returns readable text. For APIs and other content types, returns the raw response body. For JS-heavy SPAs use browser_get instead.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"method":  {Type: "string", Description: "HTTP method: GET, POST, PUT, DELETE, PATCH (default: GET)"},
					"url":     {Type: "string", Description: "The URL to request (http or https)"},
					"headers": {Type: "object", Description: "Optional HTTP headers as key/value pairs (e.g. {\"Authorization\": \"Bearer token\", \"Content-Type\": \"application/json\"})"},
					"body":    {Type: "string", Description: "Request body for POST/PUT/PATCH requests (e.g. JSON string)"},
				},
				Required: []string{"url"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "web_search",
			Description: "Search the web. Returns titles, URLs, and snippets for the top results. Use http_request or browser_get to read a full page afterwards.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"query": {Type: "string", Description: "The search query"},
				},
				Required: []string{"query"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "deep_research",
			Description: "Run multi-round, in-depth web research on a question. Iteratively plans, searches the web, reads pages, and synthesizes an evolving report until comprehensive, then returns a long Markdown report with sources. Use for open-ended questions needing many sources (comparisons, surveys, 'everything about X'); not for a single quick lookup (use web_search for that). Slow — runs several search+read+LLM rounds.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"question":   {Type: "string", Description: "The research question to investigate in depth"},
					"max_rounds": {Type: "integer", Description: "Max research rounds (default 4, max 10). More rounds = deeper but slower."},
				},
				Required: []string{"question"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "skill",
			Description: "Reusable skills library. Save a procedure you worked out OR that the user walked you through (action=add) so you can recall it reliably later; an index of every saved skill (name + when-to-use) is always in your system prompt, every conversation, with no similarity matching — unlike save_learning, which only surfaces when the new message happens to embed close to what you saved. action=get loads a skill's full steps; action=list / delete also available. If the user spends real time teaching you how to handle a kind of task, save it here, not (only) as a learning.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":      {Type: "string", Description: "add, get, list, or delete", Enum: []string{"add", "save", "update", "get", "list", "delete"}},
					"name":        {Type: "string", Description: "Skill name (required for add/get/delete)"},
					"description": {Type: "string", Description: "One-line summary (for add)"},
					"when_to_use": {Type: "string", Description: "When this skill applies, shown in the index (for add)"},
					"body":        {Type: "string", Description: "The full procedure / steps, Markdown (for add)"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "note",
			Description: "Personal notes, shared across the dashboard. action=add|list|update|delete. update/delete: copy the exact id from a fresh list call — never guess or recall one. delete ONLY the note the user explicitly asked to delete, never as cleanup or preparation for another task. Notes have a title, body (Markdown) and comma-separated tags. Use to remember free-form information the user wants kept — and proactively offer to save substantial outputs (deep_research reports, summaries, drafts) as a note so they persist in the Notes app. To embed an image (from a URL, or a file already in the workspace): save/download it under workspace/data/... (wget's path param, e.g. \"data/notes/photo.png\") — NOT elsewhere, /api/file forces text/plain and breaks images — then reference it in the body as Markdown ![alt](/data/notes/photo.png) (the /data/ prefix is what makes it a real, browsable URL).",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action": {Type: "string", Description: "add, list, update, or delete", Enum: []string{"add", "list", "update", "delete"}},
					"id":     {Type: "string", Description: "Note id from list, used for update/delete. Opaque: a number for local notes, or a file path like \"folder/Note.md\" when a Markdown vault is connected."},
					"title":  {Type: "string", Description: "Note title"},
					"body":   {Type: "string", Description: "Note body (Markdown)"},
					"tags":   {Type: "string", Description: "Comma-separated tags"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "task",
			Description: "To-do tasks (per board). action=add|list|done|reopen|delete. Tasks have a title, priority (low|normal|high) and optional due date. list hides completed tasks unless include_done=true.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":       {Type: "string", Description: "add, list, done, reopen, or delete", Enum: []string{"add", "list", "done", "reopen", "delete"}},
					"id":           {Type: "string", Description: "Task id from list, used for done/reopen/delete. Opaque: a number locally, or an object path when a CalDAV account is connected."},
					"title":        {Type: "string", Description: "Task title (for add)"},
					"priority":     {Type: "string", Description: "low, normal, or high", Enum: []string{"low", "normal", "high"}},
					"due":          {Type: "string", Description: "Due date, e.g. '2026-07-01 09:00' or '2026-07-01'"},
					"include_done": {Type: "boolean", Description: "Include completed tasks when listing"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "email",
			Description: "Email over IMAP/SMTP. action=config sets up the account (imap_host, smtp_host, user, password, optional ports/from — password is stored encrypted). Then: list (recent inbox), read (uid → full body), search (query), send (to, subject, body), reply (uid, body — threads to the original). Summarize/triage/draft using the LLM yourself from list/read output.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":    {Type: "string", Description: "config, list, read, search, send, or reply", Enum: []string{"config", "list", "read", "search", "send", "reply"}},
					"to":        {Type: "string", Description: "Recipient (send); defaults to original sender for reply"},
					"subject":   {Type: "string", Description: "Subject (send)"},
					"body":      {Type: "string", Description: "Message body (send/reply)"},
					"uid":       {Type: "integer", Description: "Message UID from list/search (read/reply)"},
					"query":     {Type: "string", Description: "Search query (search)"},
					"limit":     {Type: "integer", Description: "Max messages to return (list/search, default 20)"},
					"imap_host": {Type: "string", Description: "IMAP server host (config)"},
					"imap_port": {Type: "integer", Description: "IMAP port (config, default 993)"},
					"smtp_host": {Type: "string", Description: "SMTP server host (config)"},
					"smtp_port": {Type: "integer", Description: "SMTP port (config, default 587; 465 = implicit TLS)"},
					"user":      {Type: "string", Description: "Account username/email (config)"},
					"password":  {Type: "string", Description: "Account password — stored encrypted (config)"},
					"from":      {Type: "string", Description: "From address if different from user (config)"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "calendar",
			Description: "Calendar events (per board). action=add|list|delete. Events have a title, start (required), optional end, description and location. For list, optionally pass from/to to bound the range.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":      {Type: "string", Description: "add, list, or delete", Enum: []string{"add", "list", "delete"}},
					"id":          {Type: "string", Description: "Event id from list, used for delete. Opaque: a number locally, or an object path when a CalDAV account is connected."},
					"title":       {Type: "string", Description: "Event title (for add)"},
					"description": {Type: "string", Description: "Event description"},
					"location":    {Type: "string", Description: "Event location"},
					"start":       {Type: "string", Description: "Start time, e.g. '2026-07-01 09:00' (required for add)"},
					"end":         {Type: "string", Description: "End time (optional)"},
					"from":        {Type: "string", Description: "List filter: range start"},
					"to":          {Type: "string", Description: "List filter: range end"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "rag_search",
			Description: "Search the RAG knowledge base for relevant information from uploaded documents. Use this to answer questions based on the documentation available in a collection.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"query":      {Type: "string", Description: "The question or search query"},
					"collection": {Type: "string", Description: "Name of the document collection to search"},
					"limit":      {Type: "integer", Description: "Number of chunks to return (default: 5)"},
				},
				Required: []string{"query", "collection"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "rag_ingest",
			Description: "Add a document to a RAG collection so it can be retrieved later with rag_search. Use this for user-provided documents, reference material, or any content the user wants to query. For agent learnings use save_learning instead. Prefer source_path for files: PDF is parsed page by page, PPTX is converted to PDF first, anything else is indexed as text. ONE file per call — for a folder, list_files then ingest each file into the same collection.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"collection":  {Type: "string", Description: "Collection name (created automatically if it doesn't exist)"},
					"source":      {Type: "string", Description: "A descriptive name for this document (e.g. 'api-docs', 'meeting-notes-2025'). Defaults to the filename when source_path is used."},
					"content":     {Type: "string", Description: "The full text content to index (use for inline text). Omit when using source_path."},
					"source_path": {Type: "string", Description: "Path to ONE file inside the workspace (relative to workspace root). Directories are not accepted — ingest their files one at a time."},
				},
				Required: []string{"collection"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "rag_manage",
			Description: "Inspect or delete RAG collections. Actions: list (all collections, or the documents of one collection if collection is given), delete (one document if document is given, else the entire collection).",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":     {Type: "string", Description: "One of: list, delete", Enum: []string{"list", "delete"}},
					"collection": {Type: "string", Description: "Collection name (required for delete)"},
					"document":   {Type: "string", Description: "Document filename (delete action — omit to delete the whole collection)"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "add_attachment",
			Description: "Attach a file from the workspace to your response. Images (jpg, png, webp) are displayed inline in your reply. Other file types will be made available for download. Only use paths returned by other tools or known workspace locations — never invent paths.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"path": {Type: "string", Description: "Workspace-relative path to the file to attach"},
				},
				Required: []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "save_user_info",
			Description: "Save a fact about the user to their permanent profile. Call this whenever the user explicitly states something about themselves. Same topic overwrites the previous value.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"topic":   {Type: "string", Description: "Short stable key, e.g. 'diet', 'job', 'location', 'hobbies'"},
					"content": {Type: "string", Description: "The fact as stated by the user"},
				},
				Required: []string{"topic", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "save_learning",
			Description: "Save a one-off lesson or gotcha to the permanent agent-learnings knowledge base. Call this when the user confirms that something complex finally worked, or a non-obvious solution was found. Retrieval is NOT reliable: it's a per-turn similarity search against the new message, top 3 above a threshold, nothing if the wording doesn't match closely — no fallback, no signal on a miss. For a repeatable PROCEDURE the user is teaching you (how to handle a kind of ticket/task), use the `skill` tool instead — skills are always listed in full every conversation, with no similarity gate.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"title":   {Type: "string", Description: "Short descriptive title (e.g. 'ComfyUI install on prism-workspace')"},
					"content": {Type: "string", Description: "The full lesson: what the problem was, what failed, what finally worked, and any gotchas to remember."},
				},
				Required: []string{"title", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "search_history",
			Description: "Full-text search across ALL past conversations — every workspace, the global assistant, and Telegram. Use it to recall what was said or decided earlier (even in another session) when the current context doesn't contain it. Returns matching messages with their date and session.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"query": {Type: "string", Description: "Keywords to search for (e.g. 'postgres password', 'lisbon offsite plan')"},
					"limit": {Type: "integer", Description: "Max results (default 15)"},
				},
				Required: []string{"query"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: "register_tool",
			Description: "Write a Python script that becomes a callable tool, named after its own header — no filename to pick or keep in sync. " +
				"The script must start with exactly this comment (one line, valid JSON): " +
				"# TOOL: {\"name\":\"...\",\"description\":\"...\",\"when_to_use\":\"...\",\"usage\":\"...\",\"parameters\":{\"type\":\"object\",\"properties\":{\"arg\":{\"type\":\"string\",\"description\":\"...\"}},\"required\":[\"arg\"]}}. " +
				"Always fill \"when_to_use\" (the situations this tool is for — this is how you'll recognise it later, don't rely on the name) and \"usage\" (how to call it, gotchas, an example); both are optional but strongly recommended. " +
				"Arguments arrive as a JSON string in sys.argv[1]. Print the result to stdout. " +
				"The tool is immediately callable, by its header's \"name\", right after registration.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"code": {Type: "string", Description: "Complete Python script. Must contain a # TOOL: {...} header line (all on one line) — its \"name\" is what you'll call the tool by."},
				},
				Required: []string{"code"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "list_tools",
			Description: "List all custom tools in this workspace, each with its source-file path so you can inspect or edit it directly with read_file/write_file/edit. Tools shipped with Prism are labelled and must not be edited.",
			Parameters: ollama.ToolParameters{
				Type:       "object",
				Properties: map[string]ollama.ToolProperty{},
				Required:   []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "browser_get",
			Description: "Fetch a URL using a headless Chromium browser (Playwright) and return the page as a reader sees it. Use for JS-heavy pages, SPAs, sites that block plain HTTP, and for any local HTML file — a page whose content is drawn by scripts has no readable source. Optionally evaluate a JavaScript expression to extract specific data.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"url":    {Type: "string", Description: "URL to navigate to: http://, https://, or file:///workspace/... for a local HTML file (e.g. a chat attachment saved under file:///workspace/uploads/)"},
					"script": {Type: "string", Description: "Optional JS expression evaluated in the page context (e.g. 'document.title' or 'Array.from(document.querySelectorAll(\"h2\")).map(e=>e.textContent)'). If omitted, returns the full readable page text."},
				},
				Required: []string{"url"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "browser_act",
			Description: `Interact with a web page using a sequence of actions. Cookies persist across calls so you can log in once and reuse the session. Use for login flows, form submission, multi-step navigation, widget preview/debugging, or any site requiring interaction. Console messages (console.log, console.error, uncaught exceptions) are always captured automatically and returned as a {"action":"console","messages":[...]} entry — no special action needed.`,
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"url":     {Type: "string", Description: "Initial URL to navigate to before running actions. Omit to reuse the current session page."},
					"actions": {Type: "array", Description: `List of action objects to execute in order. Each object must have a "type" field. Supported types: navigate (url), click (selector), type (selector,text), clear (selector), select (selector,value), wait_for (selector), wait_ms (ms), screenshot, evaluate (expression), get_text (selector). Console output is captured automatically. Do NOT use "wait", "timeMs" or "console" — they do not exist.`},
				},
				Required: []string{"actions"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "notify",
			Description: "Send a notification to the dashboard (appears as a toast and in the bell). Set delay_seconds for reminders — more reliable than nohup/sleep.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"title":         {Type: "string", Description: "Short notification title"},
					"message":       {Type: "string", Description: "Optional detail message"},
					"level":         {Type: "string", Description: "Severity: info (default), success, warning, error", Enum: []string{"info", "success", "warning", "error"}},
					"delay_seconds": {Type: "integer", Description: "Delay before sending (0 = immediate, e.g. 120 = 2 minutes from now)"},
				},
				Required: []string{"title"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "request_secret",
			Description: "Request a sensitive value (API key, password, token) from the user via a secure dialog. Stored encrypted, never appears in chat. Available as an env var in exec_command (name uppercased: 'openai_key' → $OPENAI_KEY). Returns immediately if the secret already exists.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"name":        {Type: "string", Description: "Unique identifier for this secret, lowercase with underscores (e.g. 'openai_key', 'github_token')"},
					"description": {Type: "string", Description: "Human-readable description shown to the user in the dialog (e.g. 'OpenAI API key for the MCP server')"},
				},
				Required: []string{"name", "description"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "secrets",
			Description: "Manage stored secrets. Actions: list (names only, never values; includes your group's shared secrets), delete (name). To create a secret, use request_secret.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action": {Type: "string", Description: "One of: list, delete", Enum: []string{"list", "delete"}},
					"name":   {Type: "string", Description: "Secret name (delete action)"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "mcp",
			Description: "Manage remote MCP (Model Context Protocol) servers whose tools become available to you immediately. Actions: list (servers and their tools), add (name+url; for authenticated servers call request_secret first and pass its name as auth_secret), remove (name).",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"action":      {Type: "string", Description: "One of: list, add, remove", Enum: []string{"list", "add", "remove"}},
					"name":        {Type: "string", Description: "Short server name (e.g. 'github', 'linear')"},
					"url":         {Type: "string", Description: "HTTP endpoint of the MCP server (add action)"},
					"auth_secret": {Type: "string", Description: "Optional: name of a stored secret used as Bearer token (add action)"},
				},
				Required: []string{"action"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "update_system_prompt",
			Description: "Update your personality: name, tone, language, and behavior. Use this when the user asks you to change how you talk or present yourself.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"personality": {Type: "string", Description: "New personality and behavior instructions (e.g. 'You are Alex, a concise French-speaking assistant...'). This replaces the current personality section."},
				},
				Required: []string{"personality"},
			},
		},
	},
}
