package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/internal/mcp"
	"prism/internal/memory"
	"prism/internal/ollama"
	"prism/internal/rag"
)

type ToolExecutor struct {
	docker                *docker.Manager
	workspaceDir          string
	pluginDir             string
	searxngURL            string
	prismToken            string
	sessionID             string
	ragScope              string // tenant scope prefix for RAG collection names (Phase 3b)
	personalScopeOverride string // per-user scope for personal knowledge when the session id doesn't encode it
	ragStore              *rag.Store
	ragEmbedder           *rag.Embedder
	ragCaptioner          *rag.Captioner
	customMgr             *customtools.Manager
	mcpMgr                *mcp.Manager
	memStore              *memory.Store
	backend               ollama.Backend // LLM backend, used by deep_research sub-calls
	model                 string
	chatBlind             bool            // chat model is text-only (no vision) → caption widget previews as text
	headless              bool            // no dashboard UI (room/Webex/Telegram/cron) → widget tools refuse
	hiddenTools           map[string]bool // tools hidden from and denied to this session
	onPluginAdd           func(id, title, content string, cols, height int)
	onPluginRem           func(id string)
	onOpenFile            func(path string)
	onFileChange          func()
	onToolsReload         func()
	onMCPReload           func()
	onNotification        func(title, message, level string)
	onProgress            func(text string) // live tool progress streamed into the chat
	onSecretRequest       func(ctx context.Context, name, description string) error
	toolGuard             ToolGuard // optional per-caller authorization gate (e.g. Webex sender permissions)

	// Telephony (Vortex): tools the agent can request but only Vox can perform.
	// Set on voice calls; their execution is relayed instead of run locally.
	telephonyTools []ollama.Tool
	telephonyRelay TelephonyRelay
	// Vox stack for the place_call tool (agent-originated outbound calls): its API
	// base URL and HTTP Basic credentials. Empty voxURL = no telephony stack docked.
	voxURL, voxUser, voxPassword string
}

// ToolGuard authorizes a tool call before it runs. It receives the tool name and
// its already-parsed arguments and returns a non-nil error to block the call —
// the error text is fed back to the model as the tool result (see Execute), so
// the model relays the refusal to the user instead of the loop aborting.
// A nil guard (the default) allows everything, preserving the trusted
// owner-only behaviour of the browser / Telegram / HTTP callers.
type ToolGuard func(name string, args map[string]interface{}) error

// SetToolGuard installs an authorization gate consulted before every tool call.
// Used by the Webex channel to enforce per-sender permissions (RAG read, RAG
// write, arbitrary tool calls). Pass nil to allow all tools.
func (e *ToolExecutor) SetToolGuard(g ToolGuard) { e.toolGuard = g }

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

// SetChatBlind marks the chat model as text-only (no vision). When true, widget
// auto-previews are described by the vision captioner as text instead of being
// attached as screenshots the chat model cannot see.
func (e *ToolExecutor) SetChatBlind(blind bool) { e.chatBlind = blind }

// SetHeadless marks a conversation with no dashboard UI (group room, Webex,
// Telegram, cron): widget tools refuse cleanly instead of pretending to work.
func (e *ToolExecutor) SetHeadless(h bool) { e.headless = h }

// SetHiddenTools installs the set of tool names this session must not see or
// run: group-disabled tools, admin-only tools for non-admins, and the user's
// own opt-outs (Settings → Tools). Hidden tools are removed from the dynamic
// tool list and denied at Execute.
func (e *ToolExecutor) SetHiddenTools(set map[string]bool) { e.hiddenTools = set }

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

// AllDynamicTools returns custom Python tools + MCP tools combined. MCP servers
// come from two layers: the session's own servers plus the group's shared ones
// (connected by a group admin under the synthetic session id "g<id>" = ragScope),
// so both the shared agent and every member's personal agent see the group MCP.
// SetTelephony enables the voice telephony tools (transfer/message/hang up) for
// this connection and wires the relay that hands their calls to Vox. Set only on
// voice calls; unset elsewhere so the dashboard never sees them.
func (e *ToolExecutor) SetTelephony(tools []ollama.Tool, relay TelephonyRelay) {
	e.telephonyTools = tools
	e.telephonyRelay = relay
}

// SetVox wires the docked Vox stack so the place_call tool can queue an outbound
// call. Empty url = not docked; place_call then reports telephony is unavailable.
func (e *ToolExecutor) SetVox(url, user, password string) {
	e.voxURL, e.voxUser, e.voxPassword = strings.TrimRight(url, "/"), user, password
}

// HasVox reports whether a telephony stack is docked (place_call is usable).
func (e *ToolExecutor) HasVox() bool { return e.voxURL != "" }

// voxDo calls the docked phone stack's API and returns the raw response body.
// Shared by the outbound-call tools: the queue lives in Vox, we never mirror it.
func (e *ToolExecutor) voxDo(tool, method, path string, payload interface{}) ([]byte, error) {
	if !e.HasVox() {
		return nil, fmt.Errorf("no telephony stack is available")
	}
	var rdr io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rdr = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, e.voxURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.voxUser != "" {
		req.SetBasicAuth(e.voxUser, e.voxPassword)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("telephony unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s failed (HTTP %d): %s", tool, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}

// placeCall queues an agent-originated outbound call in Vox. The trigger and the
// call are decoupled by Vox's own call_tasks queue, so this returns as soon as the
// task is enqueued — the agent doesn't wait for the callee to pick up.
func (e *ToolExecutor) placeCall(args map[string]interface{}) (string, error) {
	phone, _ := args["phone_number"].(string)
	mission, _ := args["mission"].(string)
	name, _ := args["contact_name"].(string)
	phone, mission = strings.TrimSpace(phone), strings.TrimSpace(mission)
	if phone == "" || mission == "" {
		return "", fmt.Errorf("phone_number and mission are required")
	}
	if _, err := e.voxDo("place_call", "POST", "/api/calls", map[string]interface{}{
		"phone_number": phone, "contact_name": strings.TrimSpace(name), "mission": mission,
	}); err != nil {
		return "", err
	}
	who := phone
	if name != "" {
		who = name + " (" + phone + ")"
	}
	return fmt.Sprintf("Outbound call to %s has been queued. It will be placed shortly and I'll be told the outcome.", who), nil
}

// callTask mirrors the fields of a Vox call_tasks row we actually use.
type callTask struct {
	ID            int     `json:"id"`
	PhoneNumber   string  `json:"phone_number"`
	ContactName   *string `json:"contact_name"`
	Mission       string  `json:"mission"`
	ScheduledAt   *string `json:"scheduled_at"`
	Status        string  `json:"status"`
	Attempts      int     `json:"attempts"`
	MaxAttempts   int     `json:"max_attempts"`
	ResultSummary *string `json:"result_summary"`
}

// fetchCallTasks reads the outbound-call queue from Vox.
func (e *ToolExecutor) fetchCallTasks(limit int) ([]callTask, error) {
	b, err := e.voxDo("list_calls", "GET", fmt.Sprintf("/api/calls?limit=%d", limit), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []callTask `json:"items"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// listCalls shows the agent the outbound-call queue so it can answer "what calls are
// scheduled?" and, crucially, find the id it needs to cancel one.
func (e *ToolExecutor) listCalls() (string, error) {
	items, err := e.fetchCallTasks(25)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No outbound call is scheduled or has been placed.", nil
	}
	var b strings.Builder
	b.WriteString("Outbound calls (most recent first):\n")
	for _, t := range items {
		who := t.PhoneNumber
		if t.ContactName != nil && *t.ContactName != "" {
			who = *t.ContactName + " (" + t.PhoneNumber + ")"
		}
		fmt.Fprintf(&b, "- id=%d | %s | %s | try %d/%d", t.ID, who, t.Status, t.Attempts, t.MaxAttempts)
		if t.ScheduledAt != nil && *t.ScheduledAt != "" {
			fmt.Fprintf(&b, " | scheduled %s", *t.ScheduledAt)
		}
		fmt.Fprintf(&b, "\n  mission: %s\n", t.Mission)
		if t.ResultSummary != nil && *t.ResultSummary != "" {
			fmt.Fprintf(&b, "  outcome: %s\n", *t.ResultSummary)
		}
	}
	b.WriteString("\nOnly a call still 'pending' can be cancelled (cancel_call).")
	return b.String(), nil
}

// cancelCall drops a still-pending outbound call from the queue. Vox refuses to
// cancel one already being dialled — the line is live, there is nothing to undo.
func (e *ToolExecutor) cancelCall(args map[string]interface{}) (string, error) {
	id := 0
	switch v := args["call_id"].(type) {
	case float64:
		id = int(v)
	case string:
		id, _ = strconv.Atoi(strings.TrimSpace(v)) //nolint:errcheck
	}
	if id <= 0 {
		return "", fmt.Errorf("call_id is required (use list_calls to find it)")
	}
	if _, err := e.voxDo("cancel_call", "DELETE", fmt.Sprintf("/api/calls/%d", id), nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Outbound call #%d has been cancelled — it will not be placed.", id), nil
}

func (e *ToolExecutor) AllDynamicTools() []ollama.Tool {
	tools := e.CustomOllamaTools()
	tools = append(tools, e.telephonyTools...)
	// Outbound calls exist only when a Vox stack is docked (Vortex): the agent can
	// place one from any channel, list the queue, and cancel one it hasn't dialled
	// yet. Admin-only by policy — they dial real people and cost money.
	if e.HasVox() {
		tools = append(tools, OutboundCallTools...)
	}
	if e.mcpMgr != nil {
		own := e.mcpMgr.ToOllamaTools(e.sessionID)
		tools = append(tools, own...)
		seen := make(map[string]bool, len(own))
		for _, t := range own {
			seen[t.Function.Name] = true
		}
		// Personal MCP servers are connected once per user, not once per board —
		// merge them in under the user's stable scope regardless of which
		// session/board is currently active. Then the group's shared servers.
		for _, scope := range []string{e.personalScope(), e.ragScope} {
			if scope == "" || scope == e.sessionID {
				continue
			}
			for _, t := range e.mcpMgr.ToOllamaTools(scope) {
				if !seen[t.Function.Name] {
					seen[t.Function.Name] = true
					tools = append(tools, t)
				}
			}
		}
	}
	// Drop tools hidden from this session (group-disabled / admin-only /
	// user opt-outs) so the model never sees them.
	if len(e.hiddenTools) > 0 {
		kept := tools[:0]
		for _, t := range tools {
			if !e.hiddenTools[t.Function.Name] {
				kept = append(kept, t)
			}
		}
		tools = kept
	}
	return tools
}

// mcpSessionFor returns which MCP layer (this board's own session, the user's
// personal scope, or the group scope) serves the named tool, preferring the
// session's own servers. The personal-scope check is what lets a personal MCP
// server connected from one board stay reachable from every other board the
// same user opens — see personalScope's doc comment.
func (e *ToolExecutor) mcpSessionFor(name string) (string, bool) {
	if e.mcpMgr == nil {
		return "", false
	}
	if e.mcpMgr.HasTool(e.sessionID, name) {
		return e.sessionID, true
	}
	for _, scope := range []string{e.personalScope(), e.ragScope} {
		if scope != "" && scope != e.sessionID && e.mcpMgr.HasTool(scope, name) {
			return scope, true
		}
	}
	return "", false
}

func (e *ToolExecutor) SetPluginDir(dir string) { e.pluginDir = dir }

func (e *ToolExecutor) SetSessionID(id string) { e.sessionID = id }

// SetRAGScope sets the tenant scope for RAG collections (Prism heavy, Phase 3b):
// a group key ("g<id>") shared by a group's members, or a personal key
// ("u<id>"). Collection names are stored prefixed with this scope so names never
// collide across tenants (rag_chunks are keyed by collection name). Empty scope
// (legacy/single-user) stores names unprefixed, preserving old behaviour.
func (e *ToolExecutor) SetRAGScope(scope string) { e.ragScope = scope }

// col returns the storage name for a user-facing collection name (scope-prefixed).
func (e *ToolExecutor) col(name string) string { return ScopeCollection(e.ragScope, name) }

// uncol strips the scope prefix from a stored collection name for display.
func (e *ToolExecutor) uncol(name string) string { return UnscopeCollection(e.ragScope, name) }

// ScopeCollection / UnscopeCollection prefix (or strip) a tenant scope on a
// user-facing collection name, so collection names never collide across
// tenants in storage. Exported so internal/server's RAG handlers and the
// agent's own col/uncol (above) share one implementation instead of two
// hand-synchronized copies.
func ScopeCollection(scope, name string) string {
	if scope == "" || name == "" {
		return name
	}
	return scope + "--" + name
}

func UnscopeCollection(scope, name string) string {
	if scope == "" {
		return name
	}
	return strings.TrimPrefix(name, scope+"--")
}

var userSessionPrefixRe = regexp.MustCompile(`^u\d+`)

// personalScope is the scope for the agent's private knowledge about its
// interlocutor (user-profile, agent-learnings). Unlike ragScope — which is the
// GROUP for group members, so document collections are shared — what the agent
// learns about a person must stay per-user.
//
// Order matters. An explicit override wins: the browser and /api/chat know the
// authenticated user, but their session id ("default", a session name) has no
// "u<id>-" prefix, so without it a user who belongs to a group would read/write
// their PERSONAL profile under the group scope ("g<id>") — colliding with every
// other member and diverging from what their "u<id>-…" Telegram session wrote.
// Then the session-id prefix (Telegram is "u<id>-telegram"). Only shared-agent
// sessions (room-g<id>, webex-g<id>-…) fall back to ragScope: the group itself is
// the "user" those agents learn about.
func (e *ToolExecutor) personalScope() string {
	if e.personalScopeOverride != "" {
		return e.personalScopeOverride
	}
	if m := userSessionPrefixRe.FindString(e.sessionID); m != "" && strings.HasPrefix(e.sessionID, m+"-") {
		return m
	}
	return e.ragScope
}

// SetPersonalScope pins the per-user scope for personal knowledge, for callers
// that know the authenticated user but whose session id doesn't encode it.
func (e *ToolExecutor) SetPersonalScope(scope string) { e.personalScopeOverride = scope }

// pcol returns the storage name for a personal-knowledge collection.
func (e *ToolExecutor) pcol(name string) string {
	s := e.personalScope()
	if s == "" || name == "" {
		return name
	}
	return s + "--" + name
}

// userStore returns the memory store scoped to the session's personal config
// space ("u<id>:"), so integration settings the agent touches (email, calendar
// providers, vault…) are the interlocutor's own, matching the HTTP handlers'
// userStore. Shared-agent sessions scope to the group; legacy stays global.
func (e *ToolExecutor) userStore() *memory.Store {
	if e.memStore == nil {
		return nil
	}
	return e.memStore.ConfigScope(e.personalScope())
}

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

	// Hidden tools (group-disabled, admin-only for this caller, user opt-outs)
	// are denied outright — same message whoever asks.
	if e.hiddenTools[name] {
		return "", nil, fmt.Errorf("tool %q is not available in this context (disabled by policy or preferences)", name)
	}
	// Authorization gate: block unauthorized tool calls (e.g. a Webex sender who
	// lacks the required permission). The error is surfaced to the model as the
	// tool result, so it explains the refusal to the user. Denials are audited.
	if e.toolGuard != nil {
		if err := e.toolGuard(name, args); err != nil {
			if e.memStore != nil {
				e.memStore.AddUsage(ctx, 0, e.sessionID, "audit", "tool_denied",
					1, map[string]interface{}{"tool": name, "error": err.Error()})
			}
			return "", nil, err
		}
	}
	// Usage: one event per tool call (fire-and-forget).
	if e.memStore != nil {
		e.memStore.AddUsage(ctx, 0, e.sessionID, "tool_call", name, 1, nil)
	}

	// Telephony tools are performed by Vox, not here: relay and return its result
	// so the model can speak a closing line. (The guard/hidden checks above already
	// ensured this call is allowed on the voice channel.)
	if IsTelephonyTool(name) {
		if e.telephonyRelay == nil {
			return "", nil, fmt.Errorf("telephony tool %q is only available on a phone call", name)
		}
		res, err := e.telephonyRelay(name, args)
		return res, nil, err
	}

	// Outbound calls: queued in, read from and cancelled in the docked Vox stack.
	switch name {
	case "place_call":
		res, err := e.placeCall(args)
		return res, nil, err
	case "list_calls":
		res, err := e.listCalls()
		return res, nil, err
	case "cancel_call":
		res, err := e.cancelCall(args)
		return res, nil, err
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
		// Route to MCP server if the tool is provided by one (own session first,
		// then the group's shared servers).
		if sess, ok := e.mcpSessionFor(name); ok {
			return wrap(e.mcpMgr.CallTool(ctx, sess, name, rawArgs))
		}
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// ─── Existing tools ───────────────────────────────────────────────────────────

// ─── Docker service tools ─────────────────────────────────────────────────────
