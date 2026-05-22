package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/memory"
	"prism/internal/ollama"
)

// systemPromptPersonalityDefault is the editable section shown before the core prompt.
// Users can replace this by asking the agent to call update_system_prompt.
const systemPromptPersonalityDefault = `You are a general-purpose AI assistant powering a personal dashboard. You have full access to a Docker workspace container and the web.`

// systemPromptCore contains the protected technical instructions that cannot be modified.
const systemPromptCore = `

## Architecture

Prism runs as two containers sharing the /workspace volume:
- prism-server — Go backend; serves the browser, proxies workspace requests
- prism-workspace — execution environment; where exec_command, cron, custom tools, and installed software run

The browser only talks to prism-server. Access routes:
  /proxy/<port>/path  →  prism-server forwards to prism-workspace:<port>  (HTTP + WebSocket, no timeout)
  /api/tool/<name>    →  runs a Python script in prism-workspace  (2-min timeout, no streaming)
  /data/<file>        →  /workspace/widget_data/<file>
  /plugins/<id>.html  →  widget HTML files

Key consequence: exec_command runs inside prism-workspace, so "curl localhost:<port>" reaches a service directly — bypassing the proxy entirely. Always verify services through the proxy, not with exec_command curl.

**Never ask for passwords, API keys, or tokens. Always use request_secret instead.**

## Widgets

A widget is a mini web app written from scratch. It must show meaningful content — not just a button. Action buttons are fine as extras when content is already displayed.

Dark theme: bg #0e0e10  text #e8e8f0  accent #6b8afd  borders #232328  muted #9090a0  green #4dba87  red #e06c75  yellow #e5c07b
Layout: cols (1=small, 2=medium, 3=full-width), height in px.
For iframes inside a widget: width:100%; height:100%; border:none; position:absolute; inset:0;

Boilerplate:
  <style>html, body { margin:0; padding:0; height:100%; overflow:hidden; background:#0e0e10; }</style>
  <div id="root">Loading…</div>
  <script>
    async function update() {
      try {
        const data = await fetch('...').then(r => r.json());
        document.getElementById('root').innerHTML = /* render */;
      } catch(e) { document.getElementById('root').textContent = 'Error: ' + e.message; }
    }
    update(); setInterval(update, 60000);
  </script>

Reliable iframe embeds (no key needed):
- Google Maps: https://maps.google.com/maps?saddr=ORIGIN&daddr=DEST&output=embed
- OpenStreetMap: https://www.openstreetmap.org/export/embed.html?bbox=LON1,LAT1,LON2,LAT2&layer=mapnik
- YouTube: https://www.youtube.com/embed/VIDEO_ID
Most news/content sites block embedding — use fetch_url and render inline instead.

Free APIs (no key):
- Weather: https://api.open-meteo.com/v1/forecast?latitude=LAT&longitude=LON&current=temperature_2m,weathercode,windspeed_10m
- Geocoding: https://geocoding-api.open-meteo.com/v1/search?name=Paris&count=1
- Stocks/crypto: https://query1.finance.yahoo.com/v8/finance/chart/AAPL?interval=1d&range=5d
- Exchange rates: https://open.er-api.com/v6/latest/EUR
- IP geolocation: https://ipapi.co/json/
- Public holidays: https://date.nager.at/api/v3/PublicHolidays/2025/FR
- World time: https://worldtimeapi.org/api/timezone/Europe/Paris
- Directions/traffic: https://api.openrouteservice.org

### Connecting widgets to the workspace

Two patterns. Pick the right one — they are not interchangeable.

**Pattern A — Third-party server (software you INSTALL, not code) → /proxy/<port>/path**

Use this when the agent installs existing software that ships its own HTTP server: ComfyUI, Jupyter, Gradio, Apache, any app with a built-in REST API. The proxy forwards HTTP and WebSocket transparently.

Install workflow — follow all four steps:
1. Start the service bound to 0.0.0.0 (NOT 127.0.0.1 — the proxy connects from prism-server, a different container, and 127.0.0.1 will refuse it).
2. Add a @reboot cron entry immediately so the service survives container restarts:
   cron_add(name="<service>-start", schedule="@reboot", command="<start command> >> /workspace/logs/<service>.log 2>&1")
   The crontab is persisted to /workspace/.crontab and restored on every container start.
3. Verify the proxy works using fetch_url — NOT exec_command curl:
   fetch_url http://server:8080/proxy/<port>/some/endpoint
   exec_command curl localhost:<port> bypasses the proxy entirely and proves nothing for the widget.
4. Build the widget using /proxy/<port>/ for all calls:
   fetch('/proxy/<port>/api/endpoint', { method: 'POST', body: JSON.stringify(data) })
   new WebSocket('ws://' + location.host + '/proxy/<port>/ws')

**Pattern B — Custom tool (backend logic you CODE yourself) → /api/tool/<name>**

This is the answer to almost every "I need backend logic" situation. Write a Python script, register it with register_tool, call it from the widget. No server, no port, no cron. Every tool automatically gets $IDE_URL and $IDE_SESSION injected as environment variables.

Steps: 1) Write the script with a "# TOOL: {...}" header and test it with exec_command. 2) register_tool. 3) Use it from the widget:

  fetch('/api/tool/my_tool?session=SESSION_ID', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({arg: 'value'}) })
    .then(r => r.json()).then(data => {
      // use data...
    })

Hard 2-minute timeout, synchronous. A tool can also write to /workspace/widget_data/<name>.json (widget polls /data/<name>.json), POST to $IDE_URL/api/notify to push notifications, or POST to $IDE_URL/api/chat to trigger the agent.

**Never start a Flask/HTTP server for logic you wrote yourself.** A custom tool does everything Flask would — with zero infrastructure. The only valid reason to run a custom HTTP server is WebSocket streaming or persistent in-memory state across calls, both of which are rare.

**Pattern C — Polling data (cron writes, widget reads) → /workspace/widget_data/<name>.json**

For data that updates on a schedule rather than on user demand. A cron job writes the file, the widget polls /data/<name>.json.

  # cron script (or tool) writes data
  import json; json.dump({"items": [...], "updated": "..."}, open("/workspace/widget_data/feed.json", "w"))
  # widget reads it
  const data = await fetch('/data/feed.json').then(r => r.json());

### Dashboard postMessage API

  window.parent.postMessage({ type: 'openFile', path: '/workspace/file.py' }, '*')   // open in editor
  window.parent.postMessage({ type: 'sendChat', text: 'Analyse foo.py' }, '*')       // send to AI chat
  window.parent.postMessage({ type: 'notify', level: 'success', message: '…' }, '*') // toast (info/success/warning/error)

### Self-verification (mandatory after every add_widget)

1. read_file the saved widget at /workspace/.plugins/<session_id>/<id>.html — confirm valid HTML, no broken tags or scripts.
2. If it uses /api/tool/<name>, call that tool directly first (exec_command python3 /workspace/agent_tools/<name>.py '{"arg":"val"}') to confirm it returns valid JSON.
3. If it fetches /data/<name>.json, use fetch_url on that URL to confirm it returns valid JSON.
4. If it uses /proxy/<port>/, use fetch_url on http://server:8080/proxy/<port>/some/endpoint — confirm the service is reachable through the proxy, not just from exec_command.
5. Fix anything broken and call add_widget again.
6. Only tell the user the widget is ready after this check. Never use /widget/<id> — that route does not exist.

## Background work

### Notifications from cron scripts

Cron jobs run in prism-workspace and cannot call notify directly. Use the HTTP API ($IDE_URL and $IDE_SESSION are auto-injected into every cron job):

  curl -s -X POST "$IDE_URL/api/notify" \
    -H "Content-Type: application/json" \
    -d "{\"session\":\"$IDE_SESSION\",\"title\":\"Title\",\"message\":\"Details\",\"level\":\"info\"}"

Levels: info, success, warning, error.

### Web and browser tasks

web_search → fetch_url (static pages) → browser_get (JS-heavy pages) → browser_act (interactive: clicks, forms, login flows).
browser_act persists cookies per session — log in once and reuse across calls.
Screenshots are saved to /workspace/.screenshots/ and served at /screenshots/<file> — display inline: ![desc](/screenshots/file.png)

### Reminders and custom tools

One-shot reminders → notify(delay_seconds=N) — server-side, reliable. Never use nohup/sleep/curl.
After register_tool, the tool is callable by the agent AND from widgets via /api/tool/<name>. Use list_tools to confirm registration.

## User profile

When the user explicitly states a personal fact — preference, dietary restriction, job, age, hobby, location, family situation, etc. — call save_user_info immediately. Use a short stable topic key (e.g. "diet", "job", "music", "location"). Saving the same topic overwrites the previous value, so it naturally handles updates ("I moved to Lyon" → update "location").

Only store facts the user explicitly stated. Never store inferences or assumptions.

## Learning from experience

When the user confirms that something complex finally worked (e.g. a widget, a service install, an API integration), call save_learning immediately to record the lesson. Include: what the problem was, what failed, what finally worked, and any gotchas. These lessons are automatically retrieved and shown to you at the start of future conversations when relevant.`

type Event struct {
	Type    string          `json:"type"`
	Content string          `json:"content,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Output  string          `json:"output,omitempty"`
	ID      string          `json:"id,omitempty"`
	Title   string          `json:"title,omitempty"`
	Path    string          `json:"path,omitempty"`
	Cols    int             `json:"cols,omitempty"`
	Height  int             `json:"height,omitempty"`
}

const (
	// maxHistoryMessages triggers summarization when user+assistant count exceeds this.
	maxHistoryMessages = 40
	// keepRecentMessages is how many recent messages to keep after summarization.
	keepRecentMessages = 10
	// defaultSessionID is used for single-user deployments.
	defaultSessionID = "default"
)

type Agent struct {
	ollama          *ollama.Client
	executor        *ToolExecutor
	model           string
	history         []ollama.Message
	toolSeq         int // monotonically increasing tool call ID across all iterations
	disabledTools   []string
	ragCtxFn        func() string                                    // returns live RAG context block for system prompt
	mcpCtxFn        func() string                                    // returns MCP servers context block for system prompt
	userProfileFn   func() string                                    // returns full user profile for system prompt injection
	learningsCtxFn  func(ctx context.Context, query string) string   // searches agent-learnings RAG for relevant past lessons
	memStore        *memory.Store
	sessionID       string
	personality     string // editable section of the system prompt
	historyLoaded   bool   // true after first DB load
}

// SetRAGContextFn registers a callback that returns the RAG collections section
// to inject into the system prompt on every chat turn.
func (a *Agent) SetRAGContextFn(fn func() string) { a.ragCtxFn = fn }

// SetMCPContextFn registers a callback that returns the MCP servers section
// to inject into the system prompt on every chat turn.
func (a *Agent) SetMCPContextFn(fn func() string) { a.mcpCtxFn = fn }

// SetUserProfileFn registers a callback that returns the full user profile to
// inject into the system prompt on every chat turn.
func (a *Agent) SetUserProfileFn(fn func() string) { a.userProfileFn = fn }

// SetLearningsCtxFn registers a callback that searches the agent-learnings RAG
// collection and returns relevant past lessons for the current query.
func (a *Agent) SetLearningsCtxFn(fn func(ctx context.Context, query string) string) {
	a.learningsCtxFn = fn
}

// InjectNote appends a user-role message to the conversation history and saves it
// to the DB. Use this to inform the agent of UI-driven events (e.g. widget deleted
// by the user) so its context stays accurate across turns.
func (a *Agent) InjectNote(content string) {
	msg := ollama.Message{Role: "user", Content: content}
	a.history = append(a.history, msg)
	a.saveMessageToDB(context.Background(), msg)
}

// SetActiveTools stores the list of disabled tool names. buildToolList() uses
// this on every callOllama() call so tools added mid-conversation take effect immediately.
func (a *Agent) SetActiveTools(disabledNames []string) {
	a.disabledTools = disabledNames
}

// buildToolList assembles the full tool list for the current call, minus any
// disabled tools. Called on every Ollama request so dynamic tools (custom Python
// scripts, MCP tools) are always up-to-date without requiring a session restart.
func (a *Agent) buildToolList() []ollama.Tool {
	all := append(append([]ollama.Tool{}, ToolDefinitions...), a.executor.AllDynamicTools()...)
	if len(a.disabledTools) == 0 {
		return all
	}
	disabled := make(map[string]bool, len(a.disabledTools))
	for _, n := range a.disabledTools {
		disabled[n] = true
	}
	filtered := make([]ollama.Tool, 0, len(all))
	for _, t := range all {
		if !disabled[t.Function.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// New creates a new Agent. personality is the editable system prompt section loaded
// from DB by the caller; pass "" to use the default.
func New(ollamaClient *ollama.Client, executor *ToolExecutor, model string, memStore *memory.Store, personality string) *Agent {
	if personality == "" {
		personality = systemPromptPersonalityDefault
	}
	return &Agent{
		ollama:      ollamaClient,
		executor:    executor,
		model:       model,
		history:     []ollama.Message{},
		memStore:    memStore,
		sessionID:   defaultSessionID,
		personality: personality,
	}
}

// ResetHistory clears in-memory history and, if a memory store is configured, the DB history too.
func (a *Agent) ResetHistory() {
	a.history = []ollama.Message{}
	a.toolSeq = 0
	a.historyLoaded = false
	if a.memStore != nil {
		if err := a.memStore.ClearHistory(context.Background(), a.sessionID); err != nil {
			log.Printf("[agent] clear history: %v", err)
		}
	}
}

// SetSession switches the agent to a different session, resetting in-memory state.
func (a *Agent) SetSession(sessionID, personality string) {
	a.sessionID = sessionID
	if personality == "" {
		personality = systemPromptPersonalityDefault
	}
	a.personality = personality
	a.history = []ollama.Message{}
	a.toolSeq = 0
	a.historyLoaded = false
}

// loadHistoryFromDB populates a.history from the DB on first call.
func (a *Agent) loadHistoryFromDB(ctx context.Context) {
	if a.historyLoaded || a.memStore == nil {
		a.historyLoaded = true
		return
	}
	a.historyLoaded = true

	entries, err := a.memStore.LoadHistory(ctx, a.sessionID)
	if err != nil {
		log.Printf("[agent] load history: %v", err)
		return
	}
	for _, e := range entries {
		content := e.Content
		// Inject timestamp prefix on user messages only so the model can reason about time
		// (not on assistant messages, to avoid the model mimicking the pattern in its responses)
		if e.Role == "user" {
			content = fmt.Sprintf("[%s] %s", e.CreatedAt.In(agentLocation).Format("2006-01-02 15:04"), e.Content)
		}
		msg := ollama.Message{Role: e.Role, Content: content}
		if len(e.ToolCalls) > 0 && string(e.ToolCalls) != "null" {
			var tcs []ollama.ToolCall
			if json.Unmarshal(e.ToolCalls, &tcs) == nil {
				msg.ToolCalls = tcs
			}
		}
		a.history = append(a.history, msg)
	}
	log.Printf("[agent] loaded %d messages from DB", len(a.history))
}

// stripThinkingBlocks removes <thought>…</thought> and <thinking>…</thinking> blocks
// that some models include in their output as internal reasoning.
func stripThinkingBlocks(s string) string {
	for _, tag := range []string{"thought", "thinking", "think"} {
		open, close := "<"+tag+">", "</"+tag+">"
		for {
			start := strings.Index(strings.ToLower(s), open)
			if start == -1 {
				break
			}
			end := strings.Index(strings.ToLower(s[start:]), close)
			if end == -1 {
				// Unclosed tag: remove just the opening tag and keep the rest,
				// so the visible response is not silently lost.
				s = s[:start] + s[start+len(open):]
				break
			}
			s = s[:start] + s[start+end+len(close):]
		}
	}
	return strings.TrimSpace(s)
}

// saveMessageToDB persists a single message to the DB (best-effort).
func (a *Agent) saveMessageToDB(ctx context.Context, msg ollama.Message) {
	if a.memStore == nil {
		return
	}
	var toolCallsJSON json.RawMessage
	if len(msg.ToolCalls) > 0 {
		b, _ := json.Marshal(msg.ToolCalls)
		toolCallsJSON = b
	}
	if err := a.memStore.AppendMessage(ctx, a.sessionID, msg.Role, msg.Content, toolCallsJSON); err != nil {
		log.Printf("[agent] save message: %v", err)
	}
}

// agentLocation is the timezone used for all timestamps shown to the model.
// Relies on the TZ environment variable being set at the container/process level.
var agentLocation = time.Local

// buildSystemPrompt assembles the full system prompt for the current request.
// learningsCtx is a pre-fetched snippet from the agent-learnings RAG (may be empty).
func (a *Agent) buildSystemPrompt(ctx context.Context, learningsCtx string) string {
	var sb strings.Builder
	sb.WriteString(a.personality)
	sb.WriteString(systemPromptCore)

	// Inject conversation summary if any
	if a.memStore != nil {
		if summary := a.memStore.GetSummary(ctx, a.sessionID); summary != "" {
			sb.WriteString("\n\n## Context from previous conversation\n\n")
			sb.WriteString(summary)
		}
	}

	// Inject user profile (always full — small and universally relevant)
	if a.userProfileFn != nil {
		if profile := a.userProfileFn(); profile != "" {
			sb.WriteString("\n\n## User profile\n\n")
			sb.WriteString(profile)
		}
	}

	// Inject relevant past learnings from the agent-learnings RAG
	if learningsCtx != "" {
		sb.WriteString("\n\n## Lessons from past experience\n\n")
		sb.WriteString(learningsCtx)
	}

	// Inject RAG context
	if a.ragCtxFn != nil {
		if extra := a.ragCtxFn(); extra != "" {
			sb.WriteString("\n\n")
			sb.WriteString(extra)
		}
	}

	// Inject MCP server context
	if a.mcpCtxFn != nil {
		if extra := a.mcpCtxFn(); extra != "" {
			sb.WriteString("\n\n")
			sb.WriteString(extra)
		}
	}

	// Inject current date/time so the model can reason about time.
	// Explicit instruction: never output the date/time in responses.
	fmt.Fprintf(&sb, "\n\nCurrent date and time: %s. Current session ID: `%s`. Use these only as internal context — never write them in your responses. When generating widget code that calls /api/tool/ or /api/notify, always append ?session=%s to the URL.", time.Now().In(agentLocation).Format("2006-01-02 15:04"), a.sessionID, a.sessionID)

	return sb.String()
}

const maxIterations = 75

// Chat processes a user message (with optional images) and streams events.
// images is a slice of base64-encoded image strings (raw base64, no data-URL prefix).
func (a *Agent) Chat(ctx context.Context, userMsg string, images []string, events chan<- Event) {
	// Load history from DB on first call in this session
	a.loadHistoryFromDB(ctx)

	// Prefix user message with timestamp so the model can reason about time.
	// Only user messages get the prefix — assistant messages don't, to avoid the model
	// mimicking the pattern and outputting timestamps in its own responses.
	timestampedContent := fmt.Sprintf("[%s] %s", time.Now().In(agentLocation).Format("2006-01-02 15:04"), userMsg)
	userMessage := ollama.Message{Role: "user", Content: timestampedContent, Images: images}
	a.history = append(a.history, userMessage)

	// For DB: store clean content (created_at column is the canonical timestamp)
	dbContent := userMsg
	if len(images) > 0 {
		dbContent += fmt.Sprintf(" [%d image(s) attached]", len(images))
	}
	a.saveMessageToDB(ctx, ollama.Message{Role: "user", Content: dbContent})

	// Fetch relevant past learnings once per turn (before the tool loop).
	var learningsCtx string
	if a.learningsCtxFn != nil {
		learningsCtx = a.learningsCtxFn(ctx, userMsg)
	}

	for iter := 0; iter < maxIterations; iter++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		fullContent, toolCalls, err := a.callOllama(ctx, learningsCtx, events)
		if err != nil {
			events <- Event{Type: "error", Content: err.Error()}
			return
		}

		// Store assistant turn with its tool_calls (proper Ollama tool-use format)
		assistantMsg := ollama.Message{
			Role:      "assistant",
			Content:   fullContent,
			ToolCalls: toolCalls,
		}
		a.history = append(a.history, assistantMsg)
		// Strip thinking blocks before saving to DB (not user-visible content)
		dbMsg := assistantMsg
		dbMsg.Content = stripThinkingBlocks(fullContent)
		a.saveMessageToDB(ctx, dbMsg)

		if len(toolCalls) == 0 {
			events <- Event{Type: "stream_end"}
			// Trigger summarization asynchronously after the turn completes
			if a.memStore != nil {
				go a.memStore.MaybeSummarize(context.Background(), a.sessionID, a.ollama, a.model, maxHistoryMessages, keepRecentMessages)
			}
			return
		}

		// Execute tools; each result becomes a "tool" message in history
		for _, tc := range toolCalls {
			a.toolSeq++
			toolID := fmt.Sprintf("tool_%d", a.toolSeq)

			events <- Event{
				Type:  "tool_use",
				ID:    toolID,
				Tool:  tc.Function.Name,
				Input: tc.Function.Arguments,
			}

			var result string
			if tc.Function.Name == "update_system_prompt" {
				result = a.handleUpdateSystemPrompt(ctx, tc.Function.Arguments)
			} else {
				var execErr error
				result, execErr = a.executor.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
				if execErr != nil {
					result = fmt.Sprintf("Error: %v", execErr)
				}
				a.emitToolSideEffects(tc.Function.Name, tc.Function.Arguments, events)
			}

			events <- Event{
				Type:   "tool_result",
				ID:     toolID,
				Output: result,
			}

			toolMsg := ollama.Message{Role: "tool", Content: result}
			if tc.Function.Name == "browser_act" {
				toolMsg.Images = extractScreenshotImages(result, a.executor.WorkspaceDir())
			}
			a.history = append(a.history, toolMsg)
			a.saveMessageToDB(ctx, toolMsg)
		}
	}

	events <- Event{Type: "error", Content: "Max iterations reached — the agent may be looping. Start a new chat."}
}

// extractScreenshotImages parses a browser_act JSON result, reads any screenshot
// files, and returns them as base64-encoded strings for multimodal models.
func extractScreenshotImages(result, workspaceDir string) []string {
	var actions []struct {
		Action string `json:"action"`
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal([]byte(result), &actions); err != nil {
		return nil
	}
	var images []string
	for _, a := range actions {
		if a.Action != "screenshot" || a.Status != "ok" || a.URL == "" {
			continue
		}
		fname := strings.TrimPrefix(a.URL, "/screenshots/")
		data, err := os.ReadFile(filepath.Join(workspaceDir, ".screenshots", fname))
		if err != nil {
			continue
		}
		images = append(images, base64.StdEncoding.EncodeToString(data))
	}
	return images
}

// handleUpdateSystemPrompt processes the update_system_prompt tool call.
func (a *Agent) handleUpdateSystemPrompt(ctx context.Context, rawArgs json.RawMessage) string {
	var args struct {
		Personality string `json:"personality"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil || args.Personality == "" {
		return "Error: personality field is required"
	}
	a.personality = args.Personality
	if a.memStore != nil {
		if err := a.memStore.SetConfig(ctx, memory.KeyPersonality+"_"+a.sessionID, args.Personality); err != nil {
			log.Printf("[agent] save personality: %v", err)
			return "System prompt updated in memory (DB save failed: " + err.Error() + ")"
		}
	}
	return "System prompt personality updated successfully. Changes take effect on the next message."
}

func (a *Agent) callOllama(ctx context.Context, learningsCtx string, events chan<- Event) (string, []ollama.ToolCall, error) {
	prompt := a.buildSystemPrompt(ctx, learningsCtx)

	messages := append([]ollama.Message{
		{Role: "system", Content: prompt},
	}, a.history...)

	tools := a.buildToolList()
	req := ollama.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    tools,
	}

	ch := make(chan ollama.StreamEvent, 100)
	go func() {
		a.ollama.Chat(ctx, req, ch)
		close(ch)
	}()

	var contentBuilder strings.Builder
	var toolCalls []ollama.ToolCall
	var inThinking bool

	for ev := range ch {
		if ev.Err != nil {
			return contentBuilder.String(), nil, ev.Err
		}
		if ev.Thinking != "" {
			if !inThinking {
				events <- Event{Type: "stream", Content: "<think>"}
				inThinking = true
			}
			events <- Event{Type: "stream", Content: ev.Thinking}
		}
		if ev.Content != "" {
			if inThinking {
				events <- Event{Type: "stream", Content: "</think>"}
				inThinking = false
			}
			contentBuilder.WriteString(ev.Content)
			events <- Event{Type: "stream", Content: ev.Content}
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
	}
	if inThinking {
		events <- Event{Type: "stream", Content: "</think>"}
	}

	content := contentBuilder.String()
	log.Printf("[agent] iter response: content=%q tool_calls=%d", truncate(content, 120), len(toolCalls))
	for i, tc := range toolCalls {
		log.Printf("[agent]   tool[%d] %s %s", i, tc.Function.Name, truncate(string(tc.Function.Arguments), 200))
	}
	return content, toolCalls, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (a *Agent) emitToolSideEffects(toolName string, rawArgs json.RawMessage, events chan<- Event) {
	var args map[string]interface{}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return
	}
	str := func(k string) string { v, _ := args[k].(string); return v }

	// plugin_load / plugin_unload are sent directly by the server.go callbacks — no duplicate here
	switch toolName {
	case "write_file":
		events <- Event{
			Type: "file_changed",
			Path: str("path"),
		}
	}
}
