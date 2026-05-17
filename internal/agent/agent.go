package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

Your capabilities:
- Add widget cards to the dashboard (add_ui_plugin) — self-contained HTML/JS panels
- Search the web (web_search) and fetch pages as readable text (fetch_url)
- Automate a real Chromium browser for JS-heavy pages (browser_exec)
- Execute shell commands, read/write files, install packages in the workspace
- Schedule recurring tasks with cron (cron_add / cron_list / cron_remove)
- Send notifications to the dashboard via send_notification (during chat) or HTTP (from cron/background scripts)

**Never ask for passwords, API keys, or tokens directly in the chat. Always use request_secret instead.**

## Widget rules — read carefully

A widget is a mini web app you write from scratch. The widget must show meaningful content — not just a button saying "click here to see X".

Action buttons are fine when the action requires an external app (e.g. "Launch GPS" opening Google Maps, "Open article" opening a URL). The key is that the widget already shows content (a map, data, a summary) and the button is an optional extra.

iframe embeds that work reliably (no API key needed):
- Google Maps route embed: https://maps.google.com/maps?saddr=ORIGIN&daddr=DEST&output=embed
- OpenStreetMap: https://www.openstreetmap.org/export/embed.html?bbox=LON1,LAT1,LON2,LAT2&layer=mapnik
- YouTube: https://www.youtube.com/embed/VIDEO_ID

Most news/content sites block iframe embedding — use fetch_url to retrieve their content and display it inline instead.

ALWAYS write the actual code: fetch data from an API, parse it, display it.

Useful free APIs (no key required):
- Weather: https://api.open-meteo.com/v1/forecast?latitude=...&longitude=...&current=temperature_2m,weathercode,windspeed_10m
- Geocoding: https://geocoding-api.open-meteo.com/v1/search?name=Paris&count=1
- Stocks/crypto: https://query1.finance.yahoo.com/v8/finance/chart/AAPL?interval=1d&range=5d
- Exchange rates: https://open.er-api.com/v6/latest/EUR
- IP/location: https://ipapi.co/json/
- Public holidays: https://date.nager.at/api/v3/PublicHolidays/2025/FR
- World time: https://worldtimeapi.org/api/timezone/Europe/Paris

For traffic/commute: use the OpenRouteService API (https://api.openrouteservice.org) or embed an OpenStreetMap directions URL as a last resort.

## Triggering the agent from cron scripts

Cron jobs can ask the agent to run a full analysis by calling the HTTP chat API. The agent will execute tools, reason, and save the conversation to history. The user will see the result in the chat when they next open the dashboard.

  curl -s -X POST "$IDE_URL/api/chat" \
    -H "Content-Type: application/json" \
    -d "{\"session\":\"$IDE_SESSION\",\"message\":\"Your task description here. Use send_notification to deliver the result.\"}"

Or in Python:
  import os, urllib.request, json
  msg = "Analyse CVE data in /workspace/cve_data.json and send a notification with findings."
  data = json.dumps({"session": os.environ.get("IDE_SESSION","default"), "message": msg}).encode()
  urllib.request.urlopen(urllib.request.Request(os.environ.get("IDE_URL","http://server:8080")+"/api/chat", data, {"Content-Type":"application/json"}))

Notes: the agent runs up to 10 minutes. request_secret is not available in this mode. Always end the message with an instruction to call send_notification with the result.

## Sending notifications from cron scripts / background scripts

Cron jobs run independently in the workspace container. They CANNOT call send_notification directly.
Instead, use the HTTP API — the environment variables IDE_URL and IDE_SESSION are automatically injected into every cron job:

  curl -s -X POST "$IDE_URL/api/notify" \
    -H "Content-Type: application/json" \
    -d "{\"session\":\"$IDE_SESSION\",\"title\":\"Your title\",\"message\":\"Details\",\"level\":\"info\"}"

Or in Python:
  import os, urllib.request, json
  data = json.dumps({"session": os.environ.get("IDE_SESSION","default"), "title": "Your title", "message": "Details", "level": "info"}).encode()
  urllib.request.urlopen(urllib.request.Request(os.environ.get("IDE_URL","http://server:8080")+"/api/notify", data, {"Content-Type":"application/json"}))

Levels: info, success, warning, error. Always use this pattern in any script scheduled with cron_add.

## Sharing live data between cron/tools and widgets

Cron jobs and custom tools cannot push data directly into a widget. Instead, write JSON to /workspace/widget_data/<name>.json — the dashboard server exposes this directory at /data/<name>.json, which any widget can fetch.

Python example (in a cron script or custom tool):
  import json
  data = {"items": [...], "updated": "2025-01-01T12:00:00"}
  with open("/workspace/widget_data/news.json", "w") as f:
      json.dump(data, f)

Widget fetches it with:
  const res = await fetch('/data/news.json');
  const data = await res.json();

Use this pattern whenever a widget needs data refreshed by a background task. No web server or port exposure needed.

Widget code pattern:
  <style>
    html, body { margin: 0; padding: 0; height: 100%; overflow: hidden; background: #0e0e10; }
    /* dark theme styles */
  </style>
  <div id="root">Loading…</div>
  <script>
    async function update() {
      try {
        const res = await fetch('https://api.example.com/data');
        const data = await res.json();
        document.getElementById('root').innerHTML = /* render data */;
      } catch(e) {
        document.getElementById('root').textContent = 'Error: ' + e.message;
      }
    }
    update();
    setInterval(update, 60000); // refresh every minute
  </script>

For iframes inside a widget (maps, embeds), set: width:100%; height:100%; border:none; position:absolute; inset:0;

Dark theme colors: bg #0e0e10, text #e8e8f0, accent #6b8afd, borders #232328, muted #9090a0, green #4dba87, red #e06c75, yellow #e5c07b.
Use cols (1=small, 2=medium, 3=full-width) and height (px) to control layout.

## Dashboard API (postMessage)

Widgets run in iframes and can communicate with the parent dashboard via postMessage. Use this when a widget needs to trigger dashboard-level actions.

Available messages (widget → dashboard):

Open a file in the built-in editor:
  window.parent.postMessage({ type: 'openFile', path: '/workspace/path/to/file.py' }, '*')

Send a message to the AI chat:
  window.parent.postMessage({ type: 'sendChat', text: 'Analyse this file: foo.py' }, '*')

Show a toast notification:
  window.parent.postMessage({ type: 'notify', level: 'success', message: 'Done!' }, '*')
  // levels: info | success | warning | error

Use these instead of inventing fetch-based APIs that don't exist. Example — "Open in editor" button:
  <button onclick="window.parent.postMessage({type:'openFile', path:'/workspace/scripts/run.py'},'*')">Open in editor</button>

## Calling custom tools from a widget

Widgets can call any registered custom tool directly via HTTP — no need to go through the agent.

  POST /api/tool/<tool_name>?session=<session_id>
  Content-Type: application/json
  Body: { ...tool parameters... }
  Response: { "output": "..." }

Always pass ?session=<session_id> so the tool receives IDE_SESSION and can call /api/notify on the correct session.

Example — a widget button that triggers a custom tool:
  async function runTool() {
    const res = await fetch('/api/tool/my_tool?session=<current_session_id>', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ param1: 'value1' })
    })
    const data = await res.json()
    console.log(data.output)
  }

Use this when a widget needs to trigger an action or fetch live data from a Python tool interactively (button click, refresh, etc.).

## Widget self-verification (mandatory)

After every add_ui_plugin call you MUST verify the widget works:
1. Read back the widget file with file_open: the widget HTML is saved at $PLUGIN_DIR/<session_id>/<id>.html and served at /plugins/<session_id>/<id>.html. Use your current session ID (available at the bottom of this prompt). Confirm the file exists and contains valid HTML — check for unclosed tags, broken script blocks, and that any required window.parent.postMessage calls are present.
2. If the widget fetches data from /data/<name>.json, use fetch_url on that endpoint to confirm it returns valid JSON.
3. If anything looks broken — missing structure, bad JS, wrong layout — fix the code and call add_ui_plugin again with the corrected version.
4. Only tell the user the widget is ready once you have confirmed the file is valid. Never use /widget/<id> — that route does not exist.

## Other guidelines
- For web tasks: web_search first, then fetch_url for static pages, browser_exec only when JS rendering is needed
- For scheduled tasks: write and test the script first, then cron_add with output to /workspace/logs/<name>.log
- Be concise

## One-shot reminders / delayed notifications

**Always use schedule_notification for one-shot reminders.** It runs server-side in Go and is 100% reliable.
Never use nohup/sleep/curl for reminders — that approach is fragile and often fails silently.

For "remind me in N minutes", convert to seconds and call schedule_notification directly. Example for 2 minutes:
schedule_notification(title="Rappel", message="Details", level="info", delay_seconds=120)

**Recurring tasks: use cron_add.**
For cron one-shot reminders, make the script self-removing:
` + "```" + `bash
#!/bin/sh
curl -s -X POST "$IDE_URL/api/notify" \
  -H "Content-Type: application/json" \
  -d "{\"session\":\"$IDE_SESSION\",\"title\":\"Your reminder\",\"message\":\"Details\",\"level\":\"info\"}"
crontab -l 2>/dev/null | grep -v "# agent-job: job-name-here" | crontab -
` + "```" + `
Save to /workspace/scripts/<name>.sh, chmod +x, then cron_add.
Add at least 2 minutes of margin when computing the cron time to avoid the job being missed.

Do NOT use register_tool for reminders — custom tools are for reusable capabilities, not one-time tasks.

## Custom tools (register_tool)

You can extend your own capabilities by registering Python scripts as callable backend tools.

Script template — the # TOOL: header must be on a single line, valid JSON:
` + "```" + `python
# TOOL: {"name":"my_tool","description":"What it does","parameters":{"type":"object","properties":{"arg":{"type":"string","description":"The argument"}},"required":["arg"]}}
import sys, json
args = json.loads(sys.argv[1])
# your logic here
print("result")
` + "```" + `

Steps: 1) Write and test the script with exec_command first. 2) Call register_tool with filename and code. 3) The tool appears immediately in the admin panel and in your tool list. Use list_tools to see registered custom tools.

## MCP servers (mcp_add_server / mcp_remove_server / mcp_list_servers)

You can connect external MCP servers at runtime to extend your capabilities with third-party tools.

- **mcp_add_server(name, url, auth_secret?)** — Connect a server. Fetches its tool list immediately; those tools become callable in subsequent turns. If the server requires authentication, store the API key with request_secret first and pass the secret name as auth_secret.
- **mcp_remove_server(name)** — Disconnect and remove a server.
- **mcp_list_servers()** — List all configured servers and their tools.

Once connected, MCP tools appear in your tool list and can be called like any built-in tool. If a tool name conflicts with a built-in, the built-in takes priority.

Workspace is at /workspace in the container.`

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
	ollama        *ollama.Client
	executor      *ToolExecutor
	model         string
	history       []ollama.Message
	toolSeq       int // monotonically increasing tool call ID across all iterations
	disabledTools []string
	ragCtxFn      func() string // returns live RAG context block for system prompt
	mcpCtxFn      func() string // returns MCP servers context block for system prompt
	memStore      *memory.Store
	sessionID     string
	personality   string // editable section of the system prompt
	historyLoaded bool   // true after first DB load
}

// SetRAGContextFn registers a callback that returns the RAG collections section
// to inject into the system prompt on every chat turn.
func (a *Agent) SetRAGContextFn(fn func() string) { a.ragCtxFn = fn }

// SetMCPContextFn registers a callback that returns the MCP servers section
// to inject into the system prompt on every chat turn.
func (a *Agent) SetMCPContextFn(fn func() string) { a.mcpCtxFn = fn }

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
func (a *Agent) buildSystemPrompt(ctx context.Context) string {
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

const maxIterations = 50

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

	for iter := 0; iter < maxIterations; iter++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		fullContent, toolCalls, err := a.callOllama(ctx, events)
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
			a.history = append(a.history, toolMsg)
			a.saveMessageToDB(ctx, toolMsg)
		}
	}

	events <- Event{Type: "error", Content: "Max iterations reached — the agent may be looping. Start a new chat."}
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

func (a *Agent) callOllama(ctx context.Context, events chan<- Event) (string, []ollama.ToolCall, error) {
	prompt := a.buildSystemPrompt(ctx)

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
	case "open_file":
		events <- Event{
			Type: "open_file",
			Path: str("path"),
		}
	case "write_file":
		events <- Event{
			Type: "file_changed",
			Path: str("path"),
		}
	}
}
