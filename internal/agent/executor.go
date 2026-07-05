package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/internal/mcp"
	"prism/internal/memory"
	"prism/internal/ollama"
	"prism/internal/rag"
)

type ToolExecutor struct {
	docker          *docker.Manager
	workspaceDir    string
	pluginDir       string
	searxngURL      string
	prismToken      string
	sessionID       string
	ragStore        *rag.Store
	ragEmbedder     *rag.Embedder
	ragCaptioner    *rag.Captioner
	customMgr       *customtools.Manager
	mcpMgr          *mcp.Manager
	memStore        *memory.Store
	backend         ollama.Backend // LLM backend, used by deep_research sub-calls
	model           string
	onPluginAdd     func(id, title, content string, cols, height int)
	onPluginRem     func(id string)
	onOpenFile      func(path string)
	onFileChange    func()
	onToolsReload   func()
	onMCPReload     func()
	onNotification  func(title, message, level string)
	onProgress      func(text string) // live tool progress streamed into the chat
	onSecretRequest func(ctx context.Context, name, description string) error
}

func NewToolExecutor(dm *docker.Manager, workspaceDir, pluginDir, searxngURL, prismToken string) *ToolExecutor {
	return &ToolExecutor{
		docker:       dm,
		workspaceDir: workspaceDir,
		pluginDir:    pluginDir,
		searxngURL:   searxngURL,
		prismToken:   prismToken,
	}
}

// SetLLM gives the executor access to the chat backend + model so tools that
// drive their own LLM sub-calls (deep_research) can reuse the same provider.
func (e *ToolExecutor) SetLLM(backend ollama.Backend, model string) {
	e.backend = backend
	e.model = model
}

// SetProgressFn registers a callback for live tool progress (e.g. deep_research
// step-by-step), streamed into the chat.
func (e *ToolExecutor) SetProgressFn(fn func(text string)) { e.onProgress = fn }

func (e *ToolExecutor) SetRAG(store *rag.Store, embedder *rag.Embedder, captioner *rag.Captioner) {
	e.ragStore = store
	e.ragEmbedder = embedder
	e.ragCaptioner = captioner
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

func (e *ToolExecutor) SetPluginDir(dir string) { e.pluginDir = dir }

func (e *ToolExecutor) SetSessionID(id string) { e.sessionID = id }

func (e *ToolExecutor) WorkspaceDir() string { return e.workspaceDir }

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

func (e *ToolExecutor) Execute(ctx context.Context, name string, rawArgs json.RawMessage) (string, []string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", nil, fmt.Errorf("invalid args: %w", err)
	}

	str := func(key string) string {
		v, _ := args[key].(string)
		return v
	}
	wrap := func(s string, err error) (string, []string, error) {
		return s, nil, err
	}

	switch name {
	case "docker_run":
		portFloat, _ := args["port"].(float64)
		gpu, _ := args["gpu"].(bool)
		var envMap map[string]string
		if raw, ok := args["env"].(map[string]interface{}); ok {
			envMap = make(map[string]string, len(raw))
			for k, v := range raw {
				envMap[k] = fmt.Sprintf("%v", v)
			}
		}
		var volumes []string
		if raw, ok := args["volumes"].([]interface{}); ok {
			for _, v := range raw {
				volumes = append(volumes, fmt.Sprintf("%v", v))
			}
		}
		var extraPorts []int
		if raw, ok := args["extra_ports"].([]interface{}); ok {
			for _, v := range raw {
				if f, ok := v.(float64); ok {
					extraPorts = append(extraPorts, int(f))
				}
			}
		}
		return wrap(e.dockerRun(ctx, str("image"), str("name"), int(portFloat), extraPorts, str("command"), envMap, volumes, gpu, str("purpose")))
	case "docker_manage":
		switch str("action") {
		case "ps":
			return wrap(e.dockerPS(ctx))
		case "list":
			return wrap(e.dockerList(ctx))
		case "logs":
			tailFloat, _ := args["tail"].(float64)
			tail := int(tailFloat)
			if tail <= 0 {
				tail = 50
			}
			return wrap(e.dockerLogs(ctx, str("name"), tail))
		case "exec":
			return wrap(e.dockerExecService(ctx, str("name"), str("command")))
		case "stop":
			return wrap(e.dockerStop(ctx, str("name")))
		default:
			return "", nil, fmt.Errorf("docker_manage: unknown action %q (expected ps, list, logs, exec, stop)", str("action"))
		}
	// Legacy aliases — kept so existing /api/builtin callers, cron scripts and
	// widgets keep working after the tool consolidation.
	case "docker_stop":
		return wrap(e.dockerStop(ctx, str("name")))
	case "docker_ps":
		return wrap(e.dockerPS(ctx))
	case "docker_logs":
		tailFloat, _ := args["tail"].(float64)
		tail := int(tailFloat)
		if tail <= 0 {
			tail = 50
		}
		return wrap(e.dockerLogs(ctx, str("name"), tail))
	case "docker_list":
		return wrap(e.dockerList(ctx))
	case "docker_exec":
		return wrap(e.dockerExecService(ctx, str("name"), str("command")))
	case "docker_compose":
		tailFloat, _ := args["tail"].(float64)
		tail := int(tailFloat)
		if tail <= 0 {
			tail = 50
		}
		return wrap(e.dockerCompose(ctx, str("action"), str("file"), str("project"), str("service"), str("command"), tail))
	case "wget":
		return wrap(e.downloadFile(ctx, str("url"), str("path")))
	case "exec_command":
		return wrap(e.execCommand(ctx, str("command")))
	case "write_file":
		return wrap(e.writeFile(str("path"), str("content")))
	case "read_file":
		return wrap(e.readFile(str("path")))
	case "list_files":
		path := str("path")
		if path == "" {
			path = "."
		}
		return wrap(e.listFiles(path))
	case "delete_file":
		return wrap(e.deleteFile(str("path")))
	case "install_packages":
		switch str("manager") {
		case "apt":
			return wrap(e.aptInstall(ctx, str("packages")))
		case "pip":
			return wrap(e.pipInstall(ctx, str("packages")))
		default:
			return "", nil, fmt.Errorf("install_packages: unknown manager %q (expected apt or pip)", str("manager"))
		}
	case "apt_install": // legacy alias
		return wrap(e.aptInstall(ctx, str("packages")))
	case "pip_install": // legacy alias
		return wrap(e.pipInstall(ctx, str("packages")))
	case "widget":
		switch str("action") {
		case "add":
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
			return e.addUIPlugin(ctx, str("id"), str("title"), str("content"), cols, height)
		case "update":
			colsFloat, _ := args["cols"].(float64)
			heightFloat, _ := args["height"].(float64)
			return e.updateUIPlugin(ctx, str("id"), str("title"), str("content"), int(colsFloat), int(heightFloat))
		case "remove":
			return wrap(e.removeUIPlugin(str("id")))
		case "list":
			return wrap(e.listUIPlugins())
		default:
			return "", nil, fmt.Errorf("widget: unknown action %q (expected add, update, remove, list)", str("action"))
		}
	case "add_widget": // legacy alias
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
		return e.addUIPlugin(ctx, str("id"), str("title"), str("content"), cols, height)
	case "list_widgets": // legacy alias
		return wrap(e.listUIPlugins())
	case "remove_widget": // legacy alias
		return wrap(e.removeUIPlugin(str("id")))
	case "update_widget": // legacy alias
		colsFloat, _ := args["cols"].(float64)
		heightFloat, _ := args["height"].(float64)
		return e.updateUIPlugin(ctx, str("id"), str("title"), str("content"), int(colsFloat), int(heightFloat))
	case "show_in_editor":
		return wrap(e.openFile(str("path")))
	case "cron":
		switch str("action") {
		case "list":
			return wrap(e.cronList(ctx))
		case "add":
			return wrap(e.cronAdd(ctx, str("name"), str("schedule"), str("command"), str("description")))
		case "remove":
			return wrap(e.cronRemove(ctx, str("name")))
		default:
			return "", nil, fmt.Errorf("cron: unknown action %q (expected list, add, remove)", str("action"))
		}
	case "cron_list": // legacy alias
		return wrap(e.cronList(ctx))
	case "cron_add": // legacy alias
		return wrap(e.cronAdd(ctx, str("name"), str("schedule"), str("command"), str("description")))
	case "cron_remove": // legacy alias
		return wrap(e.cronRemove(ctx, str("name")))
	case "http_request":
		headersRaw, _ := args["headers"].(map[string]interface{})
		headers := make(map[string]string, len(headersRaw))
		for k, v := range headersRaw {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
		method := str("method")
		if method == "" {
			method = "GET"
		}
		return wrap(e.httpRequest(ctx, method, str("url"), headers, str("body")))
	case "web_search":
		return wrap(e.webSearch(ctx, str("query")))
	case "deep_research":
		rounds, _ := args["max_rounds"].(float64)
		return wrap(e.deepResearch(ctx, str("question"), int(rounds)))
	case "skill":
		return wrap(e.skillTool(str("action"), str("name"), str("description"), str("when_to_use"), str("body")))
	case "note":
		return wrap(e.noteTool(ctx, str("action"), idArg(args), str("title"), str("body"), str("tags")))
	case "task":
		includeDone, _ := args["include_done"].(bool)
		return wrap(e.taskTool(ctx, str("action"), idArg(args), str("title"), str("priority"), str("due"), includeDone))
	case "calendar":
		return wrap(e.calendarTool(ctx, str("action"), idArg(args), str("title"), str("description"), str("location"), str("start"), str("end"), str("from"), str("to")))
	case "email":
		return wrap(e.emailTool(ctx, args))
	case "browser_get":
		return wrap(e.browserExec(ctx, str("url"), str("script")))
	case "browser_act":
		return wrap(e.browserAct(ctx, str("url"), args["actions"]))
	case "rag_search":
		limitFloat, _ := args["limit"].(float64)
		limit := int(limitFloat)
		if limit <= 0 {
			limit = 5
		}
		return e.ragSearch(ctx, str("query"), str("collection"), limit)
	case "rag_ingest":
		return wrap(e.ragIngest(ctx, str("collection"), str("source"), str("content"), str("source_path")))
	case "rag_show_page":
		pageFloat, _ := args["page"].(float64)
		return e.ragShowPage(ctx, str("collection"), str("filename"), int(pageFloat))
	case "add_attachment":
		return e.addAttachment(str("path"))
	case "rag_manage":
		switch str("action") {
		case "list":
			if col := str("collection"); col != "" {
				return wrap(e.ragListDocuments(ctx, col))
			}
			return wrap(e.ragListCollections(ctx))
		case "delete":
			return wrap(e.ragDelete(ctx, str("collection"), str("document")))
		default:
			return "", nil, fmt.Errorf("rag_manage: unknown action %q (expected list, delete)", str("action"))
		}
	case "rag_list": // legacy alias
		if col := str("collection"); col != "" {
			return wrap(e.ragListDocuments(ctx, col))
		}
		return wrap(e.ragListCollections(ctx))
	case "rag_delete": // legacy alias
		return wrap(e.ragDelete(ctx, str("collection"), str("document")))
	case "save_user_info":
		return wrap(e.saveUserInfo(ctx, str("topic"), str("content")))
	case "save_learning":
		return wrap(e.saveLearning(ctx, str("title"), str("content")))
	case "search_history":
		limitFloat, _ := args["limit"].(float64)
		return wrap(e.searchHistory(ctx, str("query"), int(limitFloat)))
	case "notify":
		delayFloat, _ := args["delay_seconds"].(float64)
		delay := int(delayFloat)
		if delay > 0 {
			return wrap(e.scheduleNotification(str("title"), str("message"), str("level"), delay))
		}
		return wrap(e.sendNotification(str("title"), str("message"), str("level")))
	case "register_tool":
		return wrap(e.registerTool(str("filename"), str("code")))
	case "list_tools":
		return wrap(e.listTools())
	case "request_secret":
		return wrap(e.requestSecret(ctx, str("name"), str("description")))
	case "secrets":
		switch str("action") {
		case "list":
			return wrap(e.listSecrets(ctx))
		case "delete":
			return wrap(e.deleteSecret(ctx, str("name")))
		default:
			return "", nil, fmt.Errorf("secrets: unknown action %q (expected list, delete)", str("action"))
		}
	case "mcp":
		switch str("action") {
		case "list":
			return wrap(e.mcpListServers(ctx))
		case "add":
			return wrap(e.mcpAddServer(ctx, str("name"), str("url"), str("auth_secret")))
		case "remove":
			return wrap(e.mcpRemoveServer(ctx, str("name")))
		default:
			return "", nil, fmt.Errorf("mcp: unknown action %q (expected list, add, remove)", str("action"))
		}
	case "list_secrets": // legacy alias
		return wrap(e.listSecrets(ctx))
	case "delete_secret": // legacy alias
		return wrap(e.deleteSecret(ctx, str("name")))
	case "mcp_add_server": // legacy alias
		return wrap(e.mcpAddServer(ctx, str("name"), str("url"), str("auth_secret")))
	case "mcp_remove_server": // legacy alias
		return wrap(e.mcpRemoveServer(ctx, str("name")))
	case "mcp_list_servers": // legacy alias
		return wrap(e.mcpListServers(ctx))
	default:
		if e.customMgr != nil {
			if ct := e.customMgr.Get(name); ct != nil {
				return wrap(e.execCustomTool(ctx, ct, rawArgs))
			}
		}
		// Route to MCP server if the tool is provided by one
		if e.mcpMgr != nil && e.mcpMgr.HasTool(e.sessionID, name) {
			return wrap(e.mcpMgr.CallTool(ctx, e.sessionID, name, rawArgs))
		}
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// ─── Existing tools ───────────────────────────────────────────────────────────

// ─── Docker service tools ─────────────────────────────────────────────────────
