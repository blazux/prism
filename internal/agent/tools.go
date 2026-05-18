package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/internal/mcp"
	"prism/internal/memory"
	"prism/internal/ollama"
	"prism/internal/rag"

	"golang.org/x/net/html"
)

var ToolDefinitions = []ollama.Tool{
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "exec_command",
			Description: "Execute a shell command in the workspace container. Use this to run code, compile, test, etc.",
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
			Name:        "apt_install",
			Description: "Install packages in the workspace container using apt-get.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"packages": {Type: "string", Description: "Space-separated list of packages to install"},
				},
				Required: []string{"packages"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "pip_install",
			Description: "Install Python packages using pip.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"packages": {Type: "string", Description: "Space-separated list of Python packages to install"},
				},
				Required: []string{"packages"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "add_ui_plugin",
			Description: "Add a widget card to the personal dashboard. Widgets are self-contained HTML/JS panels rendered in iframes. Use cols to control width and height for card height. Match the dark theme: bg #0e0e10, text #e8e8f0, accent #6b8afd, border #232328.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"id":      {Type: "string", Description: "Unique widget ID (alphanumeric, no spaces)"},
					"title":   {Type: "string", Description: "Widget title shown in the card header"},
					"content": {Type: "string", Description: "Complete self-contained HTML for the widget (include <style> and <script> tags). Use fetch() to call external APIs. Add setInterval() for live-updating data."},
					"cols":    {Type: "integer", Description: "Grid column span: 1=small (default), 2=medium, 3=full-width"},
					"height":  {Type: "integer", Description: "Widget body height in pixels (default: 280)"},
				},
				Required: []string{"id", "title", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "remove_ui_plugin",
			Description: "Remove a plugin panel from the IDE interface.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"id": {Type: "string", Description: "Plugin ID to remove"},
				},
				Required: []string{"id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "open_file",
			Description: "Open a file in the IDE editor.",
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
			Name:        "cron_list",
			Description: "List all scheduled cron jobs in the workspace container.",
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
			Name:        "cron_add",
			Description: "Schedule a recurring task using cron. The command runs inside the workspace container. Use a descriptive name to identify the job later.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"name":     {Type: "string", Description: "Unique identifier for this job (e.g. 'daily-backup', 'sync-feed')"},
					"schedule": {Type: "string", Description: "Cron expression (e.g. '*/5 * * * *' for every 5 minutes, '0 9 * * 1-5' for weekdays at 9am)"},
					"command":  {Type: "string", Description: "Shell command to run (e.g. 'python3 /workspace/myscript.py >> /workspace/logs/myscript.log 2>&1')"},
				},
				Required: []string{"name", "schedule", "command"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "cron_remove",
			Description: "Remove a scheduled cron job by name.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"name": {Type: "string", Description: "Name of the job to remove (as given to cron_add)"},
				},
				Required: []string{"name"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "fetch_url",
			Description: "Fetch a URL and return its readable text content. Good for static pages: docs, articles, GitHub, Wikipedia, APIs. For JS-heavy SPAs use browser_exec instead.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"url": {Type: "string", Description: "The URL to fetch (http or https)"},
				},
				Required: []string{"url"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "web_search",
			Description: "Search the web. Returns titles, URLs, and snippets for the top results. Use fetch_url or browser_exec to read a full page afterwards.",
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
			Description: "Add a text document to the RAG knowledge base. The content is chunked, embedded, and stored so it can be retrieved later with rag_search. Use this to persist knowledge, lessons learned, best practices, or any reference material across sessions.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"collection":  {Type: "string", Description: "Name of the collection to store the document in (created automatically if it doesn't exist)"},
					"source":      {Type: "string", Description: "A descriptive name for this document (e.g. 'security-scanning-best-practices', 'cve-methodology')"},
					"content":     {Type: "string", Description: "The full text content to index"},
					"description": {Type: "string", Description: "Optional description of the collection (only used when creating a new collection)"},
				},
				Required: []string{"collection", "source", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "rag_list_collections",
			Description: "List all collections in the RAG knowledge base with their document and chunk counts.",
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
			Name:        "rag_list_documents",
			Description: "List all documents indexed in a specific RAG collection.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"collection": {Type: "string", Description: "Name of the collection to inspect"},
				},
				Required: []string{"collection"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: "register_tool",
			Description: "Create a new callable backend tool by writing a Python script to the agent_tools directory. " +
				"The script MUST contain exactly this comment on a single line (valid JSON, no line break): " +
				"# TOOL: {\"name\":\"...\",\"description\":\"...\",\"parameters\":{\"type\":\"object\",\"properties\":{\"arg\":{\"type\":\"string\",\"description\":\"...\"}},\"required\":[\"arg\"]}}. " +
				"The script receives its arguments as a JSON string in sys.argv[1] and must print its result to stdout. " +
				"After registration the tool appears in the admin panel and is immediately callable.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"filename": {Type: "string", Description: "Script filename, e.g. 'fetch_rss.py' (must end in .py)"},
					"code":     {Type: "string", Description: "Complete Python script. Must contain a # TOOL: {...} header line (all on one line)."},
				},
				Required: []string{"filename", "code"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "list_tools",
			Description: "List all custom tools you have created via register_tool.",
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
			Name:        "browser_exec",
			Description: "Open a URL in a headless Chromium browser (Playwright). Use for JS-heavy pages, SPAs, or sites that block plain HTTP. Optionally evaluate a JavaScript expression in the page context to extract specific data.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"url":    {Type: "string", Description: "URL to navigate to"},
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
			Description: `Interact with a web page using a sequence of actions. Session cookies and storage are persisted across calls (per session), so you can log in once and reuse the authenticated state. Use for form submission, login flows, multi-step navigation, or scraping pages that require interaction. Returns a JSON array with the result of each action. Supported action types: navigate {url}, click {selector}, type {selector, text}, clear {selector}, select {selector, value}, wait_for {selector}, screenshot {full_page?}, evaluate {expression}, get_text {selector}.`,
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"url":     {Type: "string", Description: "Initial URL to navigate to before running actions. Omit to reuse the current session page."},
					"actions": {Type: "array", Description: `List of action objects to execute in order. Each object must have a "type" field. Examples: {"type":"click","selector":"#submit"}, {"type":"type","selector":"input[name=q]","text":"hello"}, {"type":"screenshot"}, {"type":"evaluate","expression":"document.title"}, {"type":"get_text","selector":".result"}, {"type":"wait_for","selector":".loaded"}, {"type":"navigate","url":"https://..."}, {"type":"select","selector":"select#lang","value":"fr"}, {"type":"clear","selector":"#search"}.`},
				},
				Required: []string{"actions"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "schedule_notification",
			Description: "Schedule a notification to be delivered after a delay. Use this for reminders, countdowns, or any alert that should appear in the future. More reliable than nohup/sleep in a shell script.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"title":         {Type: "string", Description: "Short notification title"},
					"message":       {Type: "string", Description: "Optional detail message"},
					"level":         {Type: "string", Description: "Severity: info (default), success, warning, error"},
					"delay_seconds": {Type: "integer", Description: "Delay in seconds before the notification is sent (e.g. 120 for 2 minutes)"},
				},
				Required: []string{"title", "delay_seconds"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name: "send_notification",
			Description: "Send a notification to the dashboard. Use this to alert the user about important events, completed tasks, or monitoring results — especially from cron scripts running in the background. The notification appears as a toast and in the notification bell.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"title":   {Type: "string", Description: "Short notification title (e.g. 'Backup terminé', 'Prix AAPL : $215')"},
					"message": {Type: "string", Description: "Optional detail message"},
					"level":   {Type: "string", Description: "Severity: info (default), success, warning, error"},
				},
				Required: []string{"title"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "request_secret",
			Description: "Securely request a sensitive value (API key, password, token) from the user via a password dialog. The value is stored server-side encrypted and NEVER appears in the conversation, history, or logs. It becomes available as an environment variable in all subsequent exec_command calls. The env var name is the uppercased secret name (e.g. name='openai_key' → os.environ['OPENAI_KEY'] in Python or $OPENAI_KEY in shell). If the secret already exists, returns immediately without prompting.",
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
			Name:        "list_secrets",
			Description: "List the names of stored secrets. Only names are returned, never values.",
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
			Name:        "delete_secret",
			Description: "Delete a stored secret by name.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"name": {Type: "string", Description: "Name of the secret to delete"},
				},
				Required: []string{"name"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "mcp_add_server",
			Description: "Connect a remote MCP (Model Context Protocol) server and make its tools available to you. The server is tested immediately; its tools appear in your toolbox right away. For authenticated servers, call request_secret first to store the token, then pass its name as auth_secret.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"name":        {Type: "string", Description: "Short display name for this server (e.g. 'github', 'linear')"},
					"url":         {Type: "string", Description: "HTTP endpoint of the MCP server (e.g. https://api.githubcopilot.com/mcp/)"},
					"auth_secret": {Type: "string", Description: "Optional: name of a stored secret whose value is used as a Bearer token (e.g. 'github_token'). Use request_secret to create it first."},
				},
				Required: []string{"name", "url"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "mcp_remove_server",
			Description: "Disconnect and remove an MCP server by name.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.ToolProperty{
					"name": {Type: "string", Description: "Name of the MCP server to remove"},
				},
				Required: []string{"name"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "mcp_list_servers",
			Description: "List all configured MCP servers and their available tools.",
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
			Name: "update_system_prompt",
			Description: "Update your personality and behavior. This modifies how you present yourself and interact with the user. " +
				"IMPORTANT: Only the personality/behavior section is editable — the core technical capabilities, tools, widget rules, and API documentation are protected and cannot be changed. " +
				"Use this when the user asks you to change your tone, name, language, or general behavior.",
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

type ToolExecutor struct {
	docker           *docker.Manager
	workspaceDir     string
	pluginDir        string
	searxngURL       string
	sessionID        string
	ragStore         *rag.Store
	ragEmbedder      *rag.Embedder
	customMgr        *customtools.Manager
	mcpMgr           *mcp.Manager
	memStore         *memory.Store
	onPluginAdd      func(id, title, content string, cols, height int)
	onPluginRem      func(id string)
	onOpenFile       func(path string)
	onFileChange     func()
	onToolsReload    func()
	onMCPReload      func()
	onNotification   func(title, message, level string)
	onSecretRequest  func(ctx context.Context, name, description string) error
}

func NewToolExecutor(dm *docker.Manager, workspaceDir, pluginDir, searxngURL string) *ToolExecutor {
	return &ToolExecutor{
		docker:       dm,
		workspaceDir: workspaceDir,
		pluginDir:    pluginDir,
		searxngURL:   searxngURL,
	}
}

func (e *ToolExecutor) SetRAG(store *rag.Store, embedder *rag.Embedder) {
	e.ragStore = store
	e.ragEmbedder = embedder
}

func (e *ToolExecutor) SetCustomTools(mgr *customtools.Manager, onReload func()) {
	e.customMgr = mgr
	e.onToolsReload = onReload
}

func (e *ToolExecutor) SetMCPManager(mgr *mcp.Manager, onReload func()) {
	e.mcpMgr = mgr
	e.onMCPReload = onReload
}

func (e *ToolExecutor) CustomOllamaTools() []ollama.Tool {
	if e.customMgr == nil {
		return nil
	}
	return e.customMgr.ToOllamaTools()
}

// AllDynamicTools returns custom Python tools + MCP tools combined.
func (e *ToolExecutor) AllDynamicTools() []ollama.Tool {
	tools := e.CustomOllamaTools()
	if e.mcpMgr != nil {
		tools = append(tools, e.mcpMgr.ToOllamaTools(e.sessionID)...)
	}
	return tools
}

func (e *ToolExecutor) SetPluginDir(dir string)   { e.pluginDir = dir }
func (e *ToolExecutor) SetSessionID(id string)    { e.sessionID = id }
func (e *ToolExecutor) WorkspaceDir() string      { return e.workspaceDir }
func (e *ToolExecutor) SetMemoryStore(ms *memory.Store) { e.memStore = ms }
func (e *ToolExecutor) SetNotificationCallback(fn func(title, message, level string)) {
	e.onNotification = fn
}
func (e *ToolExecutor) SetSecretRequestCallback(fn func(ctx context.Context, name, description string) error) {
	e.onSecretRequest = fn
}

func (e *ToolExecutor) SetCallbacks(
	onAdd func(id, title, content string, cols, height int),
	onRem func(id string),
	onOpen func(path string),
	onChange func(),
) {
	e.onPluginAdd = onAdd
	e.onPluginRem = onRem
	e.onOpenFile = onOpen
	e.onFileChange = onChange
}

func (e *ToolExecutor) Execute(ctx context.Context, name string, rawArgs json.RawMessage) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	str := func(key string) string {
		v, _ := args[key].(string)
		return v
	}

	switch name {
	case "exec_command":
		return e.execCommand(ctx, str("command"))
	case "write_file":
		return e.writeFile(str("path"), str("content"))
	case "read_file":
		return e.readFile(str("path"))
	case "list_files":
		path := str("path")
		if path == "" {
			path = "."
		}
		return e.listFiles(path)
	case "apt_install":
		return e.aptInstall(ctx, str("packages"))
	case "pip_install":
		return e.pipInstall(ctx, str("packages"))
	case "add_ui_plugin":
		colsFloat, _ := args["cols"].(float64)
		cols := int(colsFloat)
		if cols < 1 || cols > 3 {
			cols = 1
		}
		heightFloat, _ := args["height"].(float64)
		height := int(heightFloat)
		if height <= 0 {
			height = 280
		}
		return e.addUIPlugin(str("id"), str("title"), str("content"), cols, height)
	case "remove_ui_plugin":
		return e.removeUIPlugin(str("id"))
	case "open_file":
		return e.openFile(str("path"))
	case "cron_list":
		return e.cronList(ctx)
	case "cron_add":
		return e.cronAdd(ctx, str("name"), str("schedule"), str("command"))
	case "cron_remove":
		return e.cronRemove(ctx, str("name"))
	case "fetch_url":
		return e.fetchURL(ctx, str("url"))
	case "web_search":
		return e.webSearch(ctx, str("query"))
	case "browser_exec":
		return e.browserExec(ctx, str("url"), str("script"))
	case "browser_act":
		return e.browserAct(ctx, str("url"), args["actions"])
	case "rag_search":
		limitFloat, _ := args["limit"].(float64)
		limit := int(limitFloat)
		if limit <= 0 {
			limit = 5
		}
		return e.ragSearch(ctx, str("query"), str("collection"), limit)
	case "rag_ingest":
		return e.ragIngest(ctx, str("collection"), str("source"), str("content"), str("description"))
	case "rag_list_collections":
		return e.ragListCollections(ctx)
	case "rag_list_documents":
		return e.ragListDocuments(ctx, str("collection"))
	case "schedule_notification":
		delayFloat, _ := args["delay_seconds"].(float64)
		return e.scheduleNotification(str("title"), str("message"), str("level"), int(delayFloat))
	case "send_notification":
		return e.sendNotification(str("title"), str("message"), str("level"))
	case "register_tool":
		return e.registerTool(str("filename"), str("code"))
	case "list_tools":
		return e.listTools()
	case "request_secret":
		return e.requestSecret(ctx, str("name"), str("description"))
	case "list_secrets":
		return e.listSecrets(ctx)
	case "delete_secret":
		return e.deleteSecret(ctx, str("name"))
	case "mcp_add_server":
		return e.mcpAddServer(ctx, str("name"), str("url"), str("auth_secret"))
	case "mcp_remove_server":
		return e.mcpRemoveServer(ctx, str("name"))
	case "mcp_list_servers":
		return e.mcpListServers(ctx)
	default:
		if e.customMgr != nil {
			if ct := e.customMgr.Get(name); ct != nil {
				return e.execCustomTool(ctx, ct, rawArgs)
			}
		}
		// Route to MCP server if the tool is provided by one
		if e.mcpMgr != nil && e.mcpMgr.HasTool(e.sessionID, name) {
			return e.mcpMgr.CallTool(ctx, e.sessionID, name, rawArgs)
		}
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// ─── Existing tools ───────────────────────────────────────────────────────────

func (e *ToolExecutor) execCommand(ctx context.Context, command string) (string, error) {
	session := e.sessionID
	if session == "" {
		session = "default"
	}
	env := map[string]string{
		"IDE_SESSION": session,
		"IDE_URL":     "http://server:8080",
	}
	if e.memStore != nil {
		if secrets, err := e.memStore.GetAllSecrets(ctx); err == nil {
			for name, value := range secrets {
				env[toEnvVarName(name)] = value
			}
		}
	}
	out, err := e.docker.ExecWithEnv(ctx, "cd /workspace && "+command, 2*time.Minute, env)
	if err != nil {
		return fmt.Sprintf("ERROR: %v\nOutput: %s", err, out), nil
	}
	if len(out) > 8000 {
		out = out[:4000] + "\n...[truncated]...\n" + out[len(out)-4000:]
	}
	return out, nil
}

// normalizeWorkspacePath strips the /workspace prefix that models often include
// when they should be passing a path relative to the workspace root.
func normalizeWorkspacePath(path string) string {
	path = strings.TrimPrefix(path, "/workspace/")
	path = strings.TrimPrefix(path, "/workspace")
	if path == "" {
		path = "."
	}
	return path
}

func (e *ToolExecutor) writeFile(path, content string) (string, error) {
	path = filepath.Clean(normalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}

	fullPath := filepath.Join(e.workspaceDir, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return "", err
	}

	if e.onFileChange != nil {
		e.onFileChange()
	}
	return fmt.Sprintf("Written %d bytes to %s", len(content), path), nil
}

func (e *ToolExecutor) readFile(path string) (string, error) {
	path = filepath.Clean(normalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}

	fullPath := filepath.Join(e.workspaceDir, path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	content := string(data)
	if len(content) > 10000 {
		content = content[:10000] + "\n...[file truncated at 10000 chars]..."
	}
	return content, nil
}

func (e *ToolExecutor) listFiles(path string) (string, error) {
	path = filepath.Clean(normalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}

	fullPath := filepath.Join(e.workspaceDir, path)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return "", err
	}

	var lines []string
	for _, entry := range entries {
		info, _ := entry.Info()
		if entry.IsDir() {
			lines = append(lines, fmt.Sprintf("DIR  %s/", entry.Name()))
		} else {
			lines = append(lines, fmt.Sprintf("FILE %s (%d bytes)", entry.Name(), info.Size()))
		}
	}
	if len(lines) == 0 {
		return "(empty directory)", nil
	}
	return strings.Join(lines, "\n"), nil
}

func (e *ToolExecutor) aptInstall(ctx context.Context, packages string) (string, error) {
	cmd := fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get update -qq && apt-get install -y -qq %s 2>&1 | tail -10", packages)
	out, err := e.docker.Exec(ctx, cmd, 5*time.Minute)
	if err != nil {
		return fmt.Sprintf("Install failed: %v\n%s", err, out), nil
	}
	return fmt.Sprintf("Installed: %s\n%s", packages, out), nil
}

func (e *ToolExecutor) pipInstall(ctx context.Context, packages string) (string, error) {
	cmd := fmt.Sprintf("pip3 install --quiet %s 2>&1 | tail -10", packages)
	out, err := e.docker.Exec(ctx, cmd, 5*time.Minute)
	if err != nil {
		return fmt.Sprintf("pip install failed: %v\n%s", err, out), nil
	}
	return fmt.Sprintf("pip installed: %s\n%s", packages, out), nil
}

type pluginMeta struct {
	Title  string `json:"title"`
	Cols   int    `json:"cols"`
	Height int    `json:"height"`
	Locked bool   `json:"locked,omitempty"`
}

func (e *ToolExecutor) addUIPlugin(id, title, content string, cols, height int) (string, error) {
	if id == "" || title == "" || content == "" {
		return "", fmt.Errorf("id, title and content are required")
	}

	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, id)

	pluginPath := filepath.Join(e.pluginDir, id+".html")
	if err := os.WriteFile(pluginPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write plugin: %w", err)
	}

	meta, _ := json.Marshal(pluginMeta{Title: title, Cols: cols, Height: height})
	os.WriteFile(filepath.Join(e.pluginDir, id+".meta.json"), meta, 0644)

	if e.onPluginAdd != nil {
		e.onPluginAdd(id, title, content, cols, height)
	}
	return fmt.Sprintf("Widget '%s' added to dashboard (cols=%d, height=%dpx)", title, cols, height), nil
}

func (e *ToolExecutor) removeUIPlugin(id string) (string, error) {
	metaPath := filepath.Join(e.pluginDir, id+".meta.json")
	htmlPath := filepath.Join(e.pluginDir, id+".html")

	_, metaErr := os.Stat(metaPath)
	_, htmlErr := os.Stat(htmlPath)
	if os.IsNotExist(metaErr) && os.IsNotExist(htmlErr) {
		return "", fmt.Errorf("widget '%s' does not exist", id)
	}

	if b, err := os.ReadFile(metaPath); err == nil {
		var m pluginMeta
		if json.Unmarshal(b, &m) == nil && m.Locked {
			return "", fmt.Errorf("widget '%s' is locked by the user and cannot be removed", id)
		}
	}
	os.Remove(htmlPath)
	os.Remove(metaPath)

	if e.onPluginRem != nil {
		e.onPluginRem(id)
	}
	return fmt.Sprintf("Plugin '%s' removed", id), nil
}

func (e *ToolExecutor) openFile(path string) (string, error) {
	if e.onOpenFile != nil {
		e.onOpenFile(path)
	}
	return fmt.Sprintf("Opened %s in editor", path), nil
}

// ─── Web tools ────────────────────────────────────────────────────────────────

func (e *ToolExecutor) fetchURL(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid URL: must start with http:// or https://")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.9")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Sprintf("HTTP %d %s", resp.StatusCode, resp.Status), nil
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "html") {
		// Non-HTML: return raw truncated content
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8000))
		return string(body), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("parse HTML: %w", err)
	}

	text := extractText(doc)
	if len(text) > 8000 {
		text = text[:8000] + "\n...[truncated]"
	}
	return text, nil
}

func (e *ToolExecutor) webSearch(ctx context.Context, query string) (string, error) {
	if e.searxngURL == "" {
		return "web_search unavailable: SEARXNG_URL not configured", nil
	}

	searchURL := e.searxngURL + "/search?q=" + url.QueryEscape(query) + "&format=json&categories=general"
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(result.Results) == 0 {
		return "No results found", nil
	}

	var sb strings.Builder
	for i, r := range result.Results {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	return sb.String(), nil
}

// browserScript is written to /workspace and run inside the workspace container.
const browserScript = `import sys, json, re
from playwright.sync_api import sync_playwright

url = sys.argv[1]
js_expr = sys.argv[2] if len(sys.argv) > 2 else None

with sync_playwright() as p:
    browser = p.chromium.launch(
        headless=True,
        args=['--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu']
    )
    page = browser.new_page()
    page.goto(url, wait_until='domcontentloaded', timeout=30000)
    page.wait_for_timeout(800)

    if js_expr:
        result = page.evaluate(js_expr)
        print(json.dumps(result, default=str))
    else:
        page.evaluate(
            "() => document.querySelectorAll('script,style,nav,footer,header,aside,noscript').forEach(e=>e.remove())"
        )
        try:
            text = page.inner_text('body')
        except Exception:
            text = page.content()
        text = re.sub(r'\n{3,}', '\n\n', text).strip()
        print(text[:8000])

    browser.close()
`

func (e *ToolExecutor) browserExec(ctx context.Context, rawURL, jsExpr string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid URL: must start with http:// or https://")
	}

	scriptPath := filepath.Join(e.workspaceDir, ".browser_exec.py")
	if err := os.WriteFile(scriptPath, []byte(browserScript), 0644); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}
	defer os.Remove(scriptPath)

	var cmd string
	if jsExpr == "" {
		cmd = fmt.Sprintf("python3 /workspace/.browser_exec.py %q 2>&1", rawURL)
	} else {
		cmd = fmt.Sprintf("python3 /workspace/.browser_exec.py %q %q 2>&1", rawURL, jsExpr)
	}

	out, err := e.docker.Exec(ctx, cmd, 60*time.Second)
	if err != nil {
		return fmt.Sprintf("browser_exec failed: %v\n%s", err, out), nil
	}
	if len(out) > 8000 {
		out = out[:8000] + "\n...[truncated]"
	}
	return out, nil
}

const browserActScript = `import json, os, time
from playwright.sync_api import sync_playwright

INPUT_FILE = os.environ['BROWSER_ACT_INPUT']
SESSION_FILE = os.environ['BROWSER_ACT_SESSION']
SCREENSHOT_DIR = '/workspace/.screenshots'
os.makedirs(SCREENSHOT_DIR, exist_ok=True)

with open(INPUT_FILE) as f:
    config = json.load(f)

start_url = config.get('url')
actions = config.get('actions') or []
results = []

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True,
        args=['--no-sandbox','--disable-dev-shm-usage','--disable-gpu'])
    ctx_opts = {}
    if os.path.exists(SESSION_FILE):
        ctx_opts['storage_state'] = SESSION_FILE
    context = browser.new_context(**ctx_opts)
    page = context.new_page()

    if start_url:
        page.goto(start_url, wait_until='domcontentloaded', timeout=30000)
        page.wait_for_timeout(500)
        results.append({'action':'navigate','status':'ok','url':page.url})

    for action in actions:
        atype = action.get('type','')
        try:
            if atype == 'navigate':
                page.goto(action['url'], wait_until='domcontentloaded', timeout=30000)
                page.wait_for_timeout(500)
                results.append({'action':'navigate','status':'ok','url':page.url})
            elif atype == 'click':
                page.click(action['selector'], timeout=10000)
                page.wait_for_timeout(300)
                results.append({'action':'click','selector':action['selector'],'status':'ok'})
            elif atype == 'type':
                page.fill(action['selector'], action['text'], timeout=10000)
                results.append({'action':'type','selector':action['selector'],'status':'ok'})
            elif atype == 'clear':
                page.fill(action['selector'], '', timeout=10000)
                results.append({'action':'clear','selector':action['selector'],'status':'ok'})
            elif atype == 'select':
                page.select_option(action['selector'], action['value'], timeout=10000)
                results.append({'action':'select','selector':action['selector'],'status':'ok'})
            elif atype == 'wait_for':
                page.wait_for_selector(action['selector'], timeout=15000)
                results.append({'action':'wait_for','selector':action['selector'],'status':'ok'})
            elif atype == 'screenshot':
                fname = str(int(time.time()*1000)) + '.png'
                path = os.path.join(SCREENSHOT_DIR, fname)
                page.screenshot(path=path, full_page=bool(action.get('full_page')))
                results.append({'action':'screenshot','status':'ok','url':'/screenshots/'+fname})
            elif atype == 'evaluate':
                result = page.evaluate(action['expression'])
                results.append({'action':'evaluate','status':'ok','result':result})
            elif atype == 'get_text':
                sel = action.get('selector','body')
                text = page.inner_text(sel, timeout=10000)
                results.append({'action':'get_text','selector':sel,'status':'ok','text':text[:4000]})
            else:
                results.append({'action':atype,'status':'error','error':'unknown action type'})
        except Exception as e:
            results.append({'action':atype,'status':'error','error':str(e)})

    context.storage_state(path=SESSION_FILE)
    context.close()
    browser.close()

print(json.dumps(results, default=str, indent=2))
`

func (e *ToolExecutor) browserAct(ctx context.Context, rawURL string, rawActions interface{}) (string, error) {
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", fmt.Errorf("invalid URL: must start with http:// or https://")
		}
	}

	sessionID := e.sessionID
	if sessionID == "" {
		sessionID = "default"
	}

	type actInput struct {
		URL     string      `json:"url,omitempty"`
		Actions interface{} `json:"actions"`
	}
	inputJSON, err := json.Marshal(actInput{URL: rawURL, Actions: rawActions})
	if err != nil {
		return "", fmt.Errorf("marshal actions: %w", err)
	}

	inputFile := filepath.Join(e.workspaceDir, ".browser_act_"+sessionID+".json")
	sessionFile := "/workspace/.browser_session_" + sessionID + ".json"

	if err := os.WriteFile(inputFile, inputJSON, 0644); err != nil {
		return "", fmt.Errorf("write input: %w", err)
	}
	defer os.Remove(inputFile)

	scriptPath := filepath.Join(e.workspaceDir, ".browser_act.py")
	if err := os.WriteFile(scriptPath, []byte(browserActScript), 0644); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	containerInput := "/workspace/.browser_act_" + sessionID + ".json"
	cmd := fmt.Sprintf(
		"BROWSER_ACT_INPUT=%s BROWSER_ACT_SESSION=%s python3 /workspace/.browser_act.py 2>&1",
		containerInput, sessionFile,
	)
	out, err := e.docker.Exec(ctx, cmd, 90*time.Second)
	if err != nil {
		return fmt.Sprintf("browser_act failed: %v\n%s", err, out), nil
	}
	if len(out) > 12000 {
		out = out[:12000] + "\n...[truncated]"
	}
	return out, nil
}

// ─── Cron tools ───────────────────────────────────────────────────────────────

func (e *ToolExecutor) cronList(ctx context.Context) (string, error) {
	out, err := e.docker.Exec(ctx, "crontab -l 2>/dev/null || echo '(no jobs scheduled)'", 10*time.Second)
	if err != nil {
		return "(no jobs scheduled)", nil
	}
	return strings.TrimSpace(out), nil
}

func (e *ToolExecutor) cronAdd(ctx context.Context, name, schedule, command string) (string, error) {
	if name == "" || schedule == "" || command == "" {
		return "", fmt.Errorf("name, schedule, and command are required")
	}
	// Reject newlines in any field to prevent crontab injection
	if strings.ContainsAny(name, "\n\r") {
		return "", fmt.Errorf("name must not contain newlines")
	}
	if strings.ContainsAny(schedule, "\n\r") {
		return "", fmt.Errorf("schedule must not contain newlines")
	}
	if strings.ContainsAny(command, "\n\r") {
		return "", fmt.Errorf("command must not contain newlines")
	}

	current, _ := e.docker.Exec(ctx, "crontab -l 2>/dev/null || true", 10*time.Second)
	current = strings.TrimSpace(current)

	marker := "# agent-job: " + name
	if strings.Contains(current, marker) {
		return "", fmt.Errorf("a job named %q already exists; use cron_remove first", name)
	}

	session := e.sessionID
	if session == "" {
		session = "default"
	}
	// Resolve $IDE_URL and $IDE_SESSION in the command now so cron doesn't
	// expand them in an empty environment (shell expands $VAR before inline
	// VAR=value assignments take effect).
	command = strings.ReplaceAll(command, "$IDE_URL", "http://server:8080")
	command = strings.ReplaceAll(command, "${IDE_URL}", "http://server:8080")
	command = strings.ReplaceAll(command, "$IDE_SESSION", session)
	command = strings.ReplaceAll(command, "${IDE_SESSION}", session)
	entry := fmt.Sprintf("%s\n%s %s", marker, schedule, command)
	var newCrontab string
	if current == "" {
		newCrontab = entry + "\n"
	} else {
		newCrontab = current + "\n" + entry + "\n"
	}

	if err := e.writeCrontab(ctx, newCrontab); err != nil {
		return fmt.Sprintf("cron_add failed: %v", err), nil
	}
	return fmt.Sprintf("Scheduled %q: %s %s", name, schedule, command), nil
}

func (e *ToolExecutor) cronRemove(ctx context.Context, name string) (string, error) {
	current, err := e.docker.Exec(ctx, "crontab -l 2>/dev/null || true", 10*time.Second)
	if err != nil || strings.TrimSpace(current) == "" {
		return "no cron jobs to remove", nil
	}

	marker := "# agent-job: " + name
	lines := strings.Split(current, "\n")
	var kept []string
	skip := false
	removed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == marker {
			skip = true
			removed = true
			continue
		}
		if skip {
			skip = false // drop the cron line immediately after the marker
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return fmt.Sprintf("no job named %q found", name), nil
	}

	newCrontab := strings.TrimSpace(strings.Join(kept, "\n"))
	if newCrontab == "" {
		if _, err := e.docker.Exec(ctx, "crontab -r 2>/dev/null || true", 10*time.Second); err != nil {
			return fmt.Sprintf("crontab -r failed: %v", err), nil
		}
	} else {
		if err := e.writeCrontab(ctx, newCrontab+"\n"); err != nil {
			return fmt.Sprintf("cron_remove failed: %v", err), nil
		}
	}
	return fmt.Sprintf("Removed job %q", name), nil
}

// writeCrontab writes content to a temp file on the shared volume and applies it via `crontab`.
func (e *ToolExecutor) writeCrontab(ctx context.Context, content string) error {
	// Write to persistent path on the shared volume so cron jobs survive container recreation.
	persistPath := filepath.Join(e.workspaceDir, ".crontab")
	if err := os.WriteFile(persistPath, []byte(content), 0600); err != nil {
		return err
	}
	_, err := e.docker.Exec(ctx, "crontab /workspace/.crontab", 10*time.Second)
	return err
}

// ─── Custom tools ─────────────────────────────────────────────────────────────

func (e *ToolExecutor) registerTool(filename, code string) (string, error) {
	if e.customMgr == nil {
		return "", fmt.Errorf("custom tools not configured")
	}
	if !strings.HasSuffix(filename, ".py") {
		return "", fmt.Errorf("filename must end in .py")
	}
	base := filepath.Base(filename)
	if strings.HasPrefix(base, ".") {
		return "", fmt.Errorf("invalid filename")
	}

	path := filepath.Join(e.customMgr.Dir(), base)
	if err := os.WriteFile(path, []byte(code), 0644); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	if e.onToolsReload != nil {
		e.onToolsReload()
	}

	name := extractToolName(code)
	if name == "" {
		return fmt.Sprintf("Script written to agent_tools/%s but no valid # TOOL: header found — check your header format.", base), nil
	}
	return fmt.Sprintf("Tool '%s' registered from %s — it now appears in the admin panel and is immediately callable.", name, base), nil
}

func (e *ToolExecutor) listTools() (string, error) {
	if e.customMgr == nil {
		return "Custom tools not configured.", nil
	}
	tools := e.customMgr.All()
	if len(tools) == 0 {
		return "No custom tools registered yet. Use register_tool to create one.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Custom tools (%d):\n", len(tools))
	for _, t := range tools {
		fmt.Fprintf(&sb, "  - %s (%s): %s\n", t.Name, t.Filename, t.Description)
	}
	return sb.String(), nil
}

func (e *ToolExecutor) execCustomTool(ctx context.Context, tool *customtools.Tool, rawArgs json.RawMessage) (string, error) {
	cmd := fmt.Sprintf("python3 /workspace/agent_tools/%s %s",
		shellEscape(tool.Filename), shellEscape(string(rawArgs)))
	return e.execCommand(ctx, cmd)
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func extractToolName(code string) string {
	for _, line := range strings.SplitN(code, "\n", 30) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# TOOL:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "# TOOL:"))
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(raw), &m) == nil {
			return m.Name
		}
	}
	return ""
}

// ─── RAG tool ─────────────────────────────────────────────────────────────────

func (e *ToolExecutor) ragSearch(ctx context.Context, query, collection string, limit int) (string, error) {
	if e.ragStore == nil || e.ragEmbedder == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if query == "" || collection == "" {
		return "", fmt.Errorf("query and collection are required")
	}

	embedding, err := e.ragEmbedder.Embed(ctx, query)
	if err != nil {
		return fmt.Sprintf("ERROR embedding query: %v", err), nil
	}

	results, err := e.ragStore.Search(ctx, collection, embedding, limit)
	if err != nil {
		return fmt.Sprintf("ERROR searching: %v", err), nil
	}
	if len(results) == 0 {
		return fmt.Sprintf("No results found in collection %q for query: %s", collection, query), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d relevant chunks in collection %q:\n\n", len(results), collection)
	for i, r := range results {
		fmt.Fprintf(&sb, "--- [%d] %s (chunk %d, score %.3f) ---\n%s\n\n", i+1, r.Filename, r.ChunkIndex, r.Score, r.Content)
	}
	return sb.String(), nil
}

func (e *ToolExecutor) ragIngest(ctx context.Context, collection, source, content, description string) (string, error) {
	if e.ragStore == nil || e.ragEmbedder == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" || source == "" || content == "" {
		return "", fmt.Errorf("collection, source, and content are required")
	}

	// Ensure collection is registered for this session
	if err := e.ragStore.EnsureCollection(ctx, collection, e.sessionID); err != nil {
		return fmt.Sprintf("ERROR registering collection: %v", err), nil
	}
	// Create/update collection description if provided
	if description != "" {
		if err := e.ragStore.SetCollectionDescription(ctx, collection, e.sessionID, description); err != nil {
			return fmt.Sprintf("ERROR setting collection description: %v", err), nil
		}
	}

	chunks := rag.SplitText(content)
	if len(chunks) == 0 {
		return "Content produced no chunks after splitting.", nil
	}

	embeddings, err := e.ragEmbedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return fmt.Sprintf("ERROR embedding content: %v", err), nil
	}

	if err := e.ragStore.UpsertDocument(ctx, collection, source, "", int64(len(content)), chunks, embeddings); err != nil {
		return fmt.Sprintf("ERROR storing document: %v", err), nil
	}

	return fmt.Sprintf("Ingested %q into collection %q: %d chunks indexed.", source, collection, len(chunks)), nil
}

func (e *ToolExecutor) ragListCollections(ctx context.Context) (string, error) {
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}

	cols, err := e.ragStore.ListCollections(ctx, e.sessionID)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	if len(cols) == 0 {
		return "No collections yet. Use rag_ingest to create one.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "RAG collections (%d):\n\n", len(cols))
	for _, c := range cols {
		fmt.Fprintf(&sb, "  • %s — %d docs, %d chunks", c.Name, c.DocCount, c.ChunkCount)
		if c.Description != "" {
			fmt.Fprintf(&sb, "\n    %s", c.Description)
		}
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func (e *ToolExecutor) ragListDocuments(ctx context.Context, collection string) (string, error) {
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}

	docs, err := e.ragStore.ListDocuments(ctx, collection)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	if len(docs) == 0 {
		return fmt.Sprintf("No documents in collection %q.", collection), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Documents in %q (%d):\n\n", collection, len(docs))
	for _, d := range docs {
		fmt.Fprintf(&sb, "  • %s — %d chunks, %d bytes, updated %s\n",
			d.Filename, d.ChunkCount, d.SizeBytes, d.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return sb.String(), nil
}

// ─── Notification tool ────────────────────────────────────────────────────────

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

	if e.memStore != nil {
		if _, exists, _ := e.memStore.GetSecret(ctx, name); exists {
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
	if e.memStore == nil {
		return "Secret store not available (Postgres not configured).", nil
	}
	names, err := e.memStore.ListSecretNames(ctx)
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
	if e.memStore == nil {
		return "Secret store not available (Postgres not configured).", nil
	}
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if err := e.memStore.DeleteSecret(ctx, name); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	return fmt.Sprintf("Secret '%s' deleted.", name), nil
}

// ─── MCP tools ────────────────────────────────────────────────────────────────

func (e *ToolExecutor) mcpAddServer(ctx context.Context, name, url, authSecret string) (string, error) {
	if e.mcpMgr == nil {
		return "MCP not available (Postgres required)", nil
	}
	if name == "" || url == "" {
		return "", fmt.Errorf("name and url are required")
	}
	tools, err := e.mcpMgr.Connect(ctx, e.sessionID, name, url, authSecret)
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
	if e.mcpMgr == nil {
		return "MCP not available", nil
	}
	if err := e.mcpMgr.Remove(ctx, e.sessionID, name); err != nil {
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
	servers, err := e.mcpMgr.List(ctx, e.sessionID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(servers) == 0 {
		return "No MCP servers configured. Use mcp_add_server to connect one.", nil
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

var skipTags = map[string]bool{
	"script": true, "style": true, "nav": true, "footer": true,
	"header": true, "aside": true, "noscript": true, "iframe": true,
	"form": true, "button": true, "svg": true, "img": true, "figure": true,
}

var blockTags = map[string]bool{
	"p": true, "div": true, "article": true, "section": true, "main": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "dt": true, "dd": true, "tr": true,
	"blockquote": true, "pre": true, "br": true, "hr": true,
}

func extractText(n *html.Node) string {
	var buf strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				buf.WriteString(t)
				buf.WriteByte(' ')
			}
			return
		}
		if n.Type != html.ElementNode {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			return
		}
		if skipTags[n.Data] {
			return
		}
		if blockTags[n.Data] {
			buf.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if blockTags[n.Data] {
			buf.WriteByte('\n')
		}
	}
	walk(n)

	result := buf.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}
