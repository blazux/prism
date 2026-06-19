package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"prism/internal/memory"
	"prism/internal/ollama"
)

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
	Images  []string        `json:"images,omitempty"`
}

const (
	// maxHistoryMessages triggers summarization when user+assistant count exceeds this.
	maxHistoryMessages = 40
	// keepRecentMessages is how many recent messages to keep after summarization.
	keepRecentMessages = 20
	// defaultSessionID is used for single-user deployments.
	defaultSessionID = "default"
)

type Agent struct {
	ollama         ollama.Backend
	executor       *ToolExecutor
	model          string
	histMu         sync.Mutex // guards history, toolSeq and historyLoaded (Chat goroutine vs WS read pump: InjectNote, ResetHistory, SetSession)
	history        []ollama.Message
	toolSeq        int // monotonically increasing tool call ID across all iterations
	disabledTools  []string
	ragCtxFn       func() string                                  // returns live RAG context block for system prompt
	mcpCtxFn       func() string                                  // returns MCP servers context block for system prompt
	userProfileFn  func() string                                  // returns full user profile for system prompt injection
	learningsCtxFn func(ctx context.Context, query string) string // searches agent-learnings RAG for relevant past lessons
	memStore       *memory.Store
	sessionID      string
	personality    string // editable section of the system prompt
	historyLoaded  bool   // true after first DB load
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
	a.histMu.Lock()
	a.history = append(a.history, msg)
	a.histMu.Unlock()
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
func New(ollamaClient ollama.Backend, executor *ToolExecutor, model string, memStore *memory.Store, personality string) *Agent {
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
	a.histMu.Lock()
	a.history = []ollama.Message{}
	a.toolSeq = 0
	a.historyLoaded = false
	a.histMu.Unlock()
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
	a.histMu.Lock()
	a.history = []ollama.Message{}
	a.toolSeq = 0
	a.historyLoaded = false
	a.histMu.Unlock()
}

// loadHistoryFromDB populates a.history from the DB on first call.
func (a *Agent) loadHistoryFromDB(ctx context.Context) {
	a.histMu.Lock()
	defer a.histMu.Unlock()
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
	a.histMu.Lock()
	a.history = append(a.history, userMessage)
	historyLen := len(a.history)
	a.histMu.Unlock()

	// For DB: store clean content (created_at column is the canonical timestamp)
	dbContent := userMsg
	if len(images) > 0 {
		dbContent += fmt.Sprintf(" [%d image(s) attached]", len(images))
	}
	a.saveMessageToDB(ctx, ollama.Message{Role: "user", Content: dbContent})

	// Fetch relevant past learnings once per turn (before the tool loop).
	// Use a short timeout so a slow embed model doesn't block the whole turn.
	var learningsCtx string
	if a.learningsCtxFn != nil {
		lctx, lcancel := context.WithTimeout(ctx, 10*time.Second)
		learningsCtx = a.learningsCtxFn(lctx, userMsg)
		lcancel()
	}

	log.Printf("[agent] session=%s calling ollama with %d history messages", a.sessionID, historyLen)

	// Loop detection: track consecutive identical (tool, args, result) triples.
	// If the same call produces the same result 3 times in a row, the agent is stuck.
	var (
		lastLoopKey    string
		lastLoopResult string
		loopCount      int
	)

	var emptyRetried bool
	for iter := 0; iter < maxIterations; iter++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		fullContent, toolCalls, err := a.callOllama(ctx, learningsCtx, events)
		if err != nil {
			// Intentional cancel (user clicked stop, sent new message, or closed tab):
			// close the bubble cleanly without showing an error message.
			if errors.Is(err, context.Canceled) {
				events <- Event{Type: "stream_end"}
			} else {
				events <- Event{Type: "error", Content: err.Error()}
			}
			return
		}

		// Empty response: don't save the ghost message; retry once with a nudge.
		if strings.TrimSpace(fullContent) == "" && len(toolCalls) == 0 {
			if !emptyRetried {
				log.Printf("[agent] empty response from model — retrying with 'continue'")
				emptyRetried = true
				a.histMu.Lock()
				a.history = append(a.history, ollama.Message{Role: "user", Content: "continue"})
				a.histMu.Unlock()
				continue
			}
			log.Printf("[agent] empty response from model again after retry — stopping")
			events <- Event{Type: "stream_end"}
			return
		}
		emptyRetried = false

		// Store assistant turn with its tool_calls (proper Ollama tool-use format)
		assistantMsg := ollama.Message{
			Role:      "assistant",
			Content:   fullContent,
			ToolCalls: toolCalls,
		}
		a.histMu.Lock()
		a.history = append(a.history, assistantMsg)
		a.histMu.Unlock()
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
			a.histMu.Lock()
			a.toolSeq++
			toolID := fmt.Sprintf("tool_%d", a.toolSeq)
			a.histMu.Unlock()

			events <- Event{
				Type:  "tool_use",
				ID:    toolID,
				Tool:  tc.Function.Name,
				Input: tc.Function.Arguments,
			}

			var result string
			var toolImages []string
			if tc.Function.Name == "update_system_prompt" {
				result = a.handleUpdateSystemPrompt(ctx, tc.Function.Arguments)
			} else {
				var execErr error
				result, toolImages, execErr = a.executor.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
				if execErr != nil {
					result = fmt.Sprintf("Error: %v", execErr)
				}
				a.emitToolSideEffects(tc.Function.Name, tc.Function.Arguments, events)
			}

			events <- Event{
				Type:   "tool_result",
				ID:     toolID,
				Output: result,
				Images: toolImages,
			}
			if tc.Function.Name == "add_attachment" && len(toolImages) > 0 {
				events <- Event{Type: "attachment", Images: toolImages}
			}

			// Detect retry loops: same tool + same args + same result 3 times in a row.
			loopKey := tc.Function.Name + "\x00" + string(tc.Function.Arguments)
			if loopKey == lastLoopKey && result == lastLoopResult {
				loopCount++
			} else {
				loopCount = 0
				lastLoopKey = loopKey
				lastLoopResult = result
			}
			if loopCount >= 2 {
				events <- Event{Type: "error", Content: fmt.Sprintf(
					"Loop detected: tool %q was called with identical arguments and returned the same result %d times in a row. Stopping to avoid an infinite loop.",
					tc.Function.Name, loopCount+1,
				)}
				return
			}

			toolMsg := ollama.Message{Role: "tool", Content: result}
			if tc.Function.Name == "browser_act" {
				toolMsg.Images = extractScreenshotImages(result, a.executor.WorkspaceDir())
			} else if len(toolImages) > 0 {
				toolMsg.Images = toolImages
			}
			a.histMu.Lock()
			a.history = append(a.history, toolMsg)
			a.histMu.Unlock()
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

	a.histMu.Lock()
	messages := append([]ollama.Message{
		{Role: "system", Content: prompt},
	}, a.history...)
	a.histMu.Unlock()

	tools := a.buildToolList()
	req := ollama.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    tools,
	}

	log.Printf("[agent] → ollama: %d messages, %d tools, prompt_len=%d", len(messages), len(tools), len(prompt))

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
