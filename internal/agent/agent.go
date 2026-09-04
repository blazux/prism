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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	IsError bool            `json:"is_error,omitempty"` // tool_result: the tool failed (Output holds the error text)
}

const (
	// maxHistoryMessages triggers summarization when user+assistant count exceeds this.
	maxHistoryMessages = 40
	// keepRecentMessages is how many recent messages to keep after summarization.
	keepRecentMessages = 20
	// defaultSessionID is used for single-user deployments.
	defaultSessionID = "default"
	// telegramSessionID is the reserved session for the Telegram bridge.
	telegramSessionID = "telegram"
)

// liveContextCharBudget is roughly how large Agent.history's total content is
// allowed to get before compactLiveContextIfNeeded kicks in. This is
// deliberately independent of — and not synced with — MaybeSummarize's
// DB-side thresholds above: that's the agent's long-term memory, summarized
// on its own schedule, and it's fine if what it keeps diverges from what the
// live chat keeps. This constant governs what actually gets replayed to the
// LLM on every call (see callOllama) — a long-running session (same Agent,
// many turns) would otherwise grow this unbounded until the backend rejects
// the prompt as too long, with "new chat" as the only recourse. Character-
// based like every other size cap in this codebase (no tokenizer dependency
// anywhere here). Override via LIVE_CONTEXT_CHAR_BUDGET for a deployment
// running a model with a known-larger/smaller context.
var liveContextCharBudget = func() int {
	if v := os.Getenv("LIVE_CONTEXT_CHAR_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 150_000
}()

// num_ctx now lives with the Ollama client (ollama.NumCtx), which both sets it
// on requests and derives the history budget from it via ContextBudgetChars —
// see effectiveHistoryBudget below.

type Agent struct {
	ollama          ollama.Backend
	executor        *ToolExecutor
	model           string
	histMu          sync.Mutex // guards history, toolSeq and historyLoaded (Chat goroutine vs WS read pump: InjectNote, ResetHistory, SetSession)
	history         []ollama.Message
	toolSeq         int // monotonically increasing tool call ID across all iterations
	disabledTools   []string
	ragCtxFn        func() string                                  // returns live RAG context block for system prompt
	mcpCtxFn        func() string                                  // returns MCP servers context block for system prompt
	userProfileFn   func() string                                  // returns full user profile for system prompt injection
	skillsCtxFn     func() string                                  // returns saved-skills index for system prompt
	servicesCtxFn   func() string                                  // returns the live index of agent-deployed docker services
	viewCtxFn       func() string                                  // returns what the user is currently looking at
	globalCtxFn     func() string                                  // global assistant: overview of all workspaces
	learningsCtxFn  func(ctx context.Context, query string) string // searches agent-learnings RAG for relevant past lessons
	memStore        *memory.Store
	sessionID       string
	personality     string // this session's own editable section (the adaptation, or the base for the default session)
	basePersonality string // default personality prepended for non-default sessions (layered model)
	agentName       string // optional global agent name injected into the prompt
	// limits are the per-turn budget (iteration cap, extended reasoning) loaded
	// from config by loadProfile each turn; limitsOverride is set by headless
	// callers (the group's shared agent) whose config lives elsewhere and wins
	// field by field over the config values. See Limits.
	limits              Limits
	limitsOverride      Limits
	turnThinking        bool   // resolved by Chat at turn start; read where requests are built
	turnReasoningEffort string // idem
	// Manual tool approval (dashboard only). approvalNeededFn reads the live
	// toggle at every tool call, so flipping it mid-turn applies to the next
	// call; awaitApprovalFn blocks until the user's verdict (or ctx cancel).
	// Both nil on headless surfaces (cron, Telegram, Webex…) = auto-approve.
	approvalNeededFn func() bool
	awaitApprovalFn  func(ctx context.Context, toolID string) bool
	historyLoaded    bool // true after first DB load
	// historyGen counts every external mutation of history (InjectNote,
	// ResetHistory, SetSession) — bumped under histMu. compactLiveContextIfNeeded
	// captures it before releasing the lock for its ~20s best-effort LLM
	// summarization call, then checks it again before applying the compacted
	// result: if a concurrent InjectNote/ResetHistory/SetSession landed in
	// that window, the generation no longer matches and the stale compaction
	// is discarded instead of clobbering what changed in the meantime.
	historyGen int
	// channel is the surface this turn arrives from ("" = dashboard/browser,
	// "voice" = a phone call docked from Vox). It changes the *form* of the reply
	// (spoken, short, no markup) and disables extended reasoning — never the
	// agent's identity, which stays the same across channels.
	channel string
}

// Limits bounds one turn of the agent loop. Zero values mean "not set": the
// config value applies, then the built-in default.
type Limits struct {
	// MaxIterations caps the number of model calls in one turn (each tool call
	// costs one). 0 = config / DefaultMaxIterations.
	MaxIterations int
	// Thinking controls extended reasoning (<think> / reasoning channel) for
	// backends that expose the switch. nil = config / on.
	Thinking *bool
	// LeanPrompt picks the lean system-prompt profile (for frontier models:
	// drops the small-model scaffolding — see the prompt-profiles comment in
	// prompt.go). nil = config / guided.
	LeanPrompt *bool
	// ReasoningEffort bounds the reasoning budget of thinking models
	// ("low"/"medium"/"high"/"xhigh"). "" = config / server default
	// (OPENAI_REASONING_EFFORT).
	ReasoningEffort string
}

const (
	DefaultMaxIterations = 75
	MinMaxIterations     = 10
	MaxMaxIterations     = 500
)

// ClampIterations bounds a requested iteration cap; 0 stays 0 ("use default").
func ClampIterations(n int) int {
	switch {
	case n <= 0:
		return 0
	case n < MinMaxIterations:
		return MinMaxIterations
	case n > MaxMaxIterations:
		return MaxMaxIterations
	}
	return n
}

// ReasoningEfforts lists the reasoning_effort values Settings may pick from.
// Which ones a given model accepts is up to the backend (gpt-oss: low/medium/
// high; Qwen3.8-Flash-Next: low/medium/xhigh) — an unsupported value comes back
// as the backend's own error, which is the honest signal to pick another.
var ReasoningEfforts = []string{"low", "medium", "high", "xhigh"}

// NormalizeReasoningEffort maps user input onto ReasoningEfforts; anything else
// (including empty) is "" = use the server default.
func NormalizeReasoningEffort(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, v := range ReasoningEfforts {
		if s == v {
			return v
		}
	}
	return ""
}

// SetLimits overrides the config-derived turn limits for this agent. Used by
// headless runs of a group's shared agent, whose budget is set by the group
// admin in room_config rather than in a user's config scope.
func (a *Agent) SetLimits(l Limits) { a.limitsOverride = l }

// effectiveLimits resolves override → config → default.
func (a *Agent) effectiveLimits() (maxIter int, thinking bool) {
	maxIter = a.limitsOverride.MaxIterations
	if maxIter == 0 {
		maxIter = a.limits.MaxIterations
	}
	if maxIter == 0 {
		maxIter = DefaultMaxIterations
	}
	thinking = true
	switch {
	case a.limitsOverride.Thinking != nil:
		thinking = *a.limitsOverride.Thinking
	case a.limits.Thinking != nil:
		thinking = *a.limits.Thinking
	}
	return maxIter, thinking
}

// leanPrompt resolves the prompt profile the same way effectiveLimits resolves
// the budget: override → config → guided (false).
func (a *Agent) leanPrompt() bool {
	switch {
	case a.limitsOverride.LeanPrompt != nil:
		return *a.limitsOverride.LeanPrompt
	case a.limits.LeanPrompt != nil:
		return *a.limits.LeanPrompt
	}
	return false
}

// reasoningEffort resolves the reasoning budget the same way: override →
// config → "" (the backend's default applies).
func (a *Agent) reasoningEffort() string {
	if a.limitsOverride.ReasoningEffort != "" {
		return a.limitsOverride.ReasoningEffort
	}
	return a.limits.ReasoningEffort
}

// SetChannel declares the surface the next turn comes from. See Agent.channel.
func (a *Agent) SetChannel(ch string) { a.channel = ch }

// SetApprovalFns wires manual tool approval: needed is consulted before every
// tool call, await blocks for the user's verdict on one call. See the fields.
func (a *Agent) SetApprovalFns(needed func() bool, await func(context.Context, string) bool) {
	a.approvalNeededFn = needed
	a.awaitApprovalFn = await
}

// voiceChannel is the phone surface (Vortex megazord: Vox → Cortex).
const voiceChannel = "voice"

// SetRAGContextFn registers a callback that returns the RAG collections section
// to inject into the system prompt on every chat turn.
func (a *Agent) SetRAGContextFn(fn func() string) { a.ragCtxFn = fn }

// SetMCPContextFn registers a callback that returns the MCP servers section
// to inject into the system prompt on every chat turn.
func (a *Agent) SetMCPContextFn(fn func() string) { a.mcpCtxFn = fn }

// SetUserProfileFn registers a callback that returns the full user profile to
// inject into the system prompt on every chat turn.
func (a *Agent) SetUserProfileFn(fn func() string) { a.userProfileFn = fn }

// SetSkillsContextFn registers a callback that returns the saved-skills index
// to inject into the system prompt on every chat turn.
func (a *Agent) SetSkillsContextFn(fn func() string) { a.skillsCtxFn = fn }

// SetServicesContextFn registers a callback that returns a live index of the
// docker services the agent has deployed (name, status, purpose), injected into
// the system prompt so the agent always knows what it is running rather than
// guessing.
func (a *Agent) SetServicesContextFn(fn func() string) { a.servicesCtxFn = fn }

// SetViewContextFn registers a callback that returns a description of what the
// user is currently looking at (which app/workspace, the open email/note…), so
// "summarize this" / "reply to it" resolve without the user spelling it out.
func (a *Agent) SetViewContextFn(fn func() string) { a.viewCtxFn = fn }

// SetGlobalContextFn registers a callback that returns an overview of every
// workspace. It is only wired for the global "Assistant" session — the soft
// partition: per-workspace agents don't get cross-workspace visibility.
func (a *Agent) SetGlobalContextFn(fn func() string) { a.globalCtxFn = fn }

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
	a.historyGen++
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
	a := &Agent{
		ollama:      ollamaClient,
		executor:    executor,
		model:       model,
		history:     []ollama.Message{},
		memStore:    memStore,
		sessionID:   defaultSessionID,
		personality: personality,
	}
	a.loadProfile()
	return a
}

// loadProfile loads the agent name and base personality for this session's
// owner. Sessions are user-namespaced ("u<id>-…"), so the identity keys live in
// that user's config scope — each user names and shapes their own agent.
// Un-prefixed sessions (legacy single-user, shared agents) read the global keys.
func (a *Agent) loadProfile() {
	a.basePersonality = ""
	a.agentName = ""
	a.limits = Limits{}
	if a.memStore == nil {
		return
	}
	store := a.memStore
	if m := userSessionPrefixRe.FindString(a.sessionID); m != "" && strings.HasPrefix(a.sessionID, m+"-") {
		store = store.ConfigScope(m)
	}
	ctx := context.Background()
	if name, ok, err := store.GetConfig(ctx, memory.KeyAgentName); err == nil && ok {
		a.agentName = name
	}
	// The base persona is layered under every session (including the default one).
	if base, ok, err := store.GetConfig(ctx, memory.KeyPersonalityBase); err == nil && ok {
		a.basePersonality = base
	}
	// Turn budget — edited in Settings, so re-read per turn like the persona.
	if v, ok, err := store.GetConfig(ctx, memory.KeyAgentMaxIterations); err == nil && ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			a.limits.MaxIterations = ClampIterations(n)
		}
	}
	if v, ok, err := store.GetConfig(ctx, memory.KeyAgentThinking); err == nil && ok {
		if t := strings.TrimSpace(v); t != "" {
			on := t != "off" && t != "false" && t != "0"
			a.limits.Thinking = &on
		}
	}
	if v, ok, err := store.GetConfig(ctx, memory.KeyAgentLeanPrompt); err == nil && ok {
		if t := strings.TrimSpace(v); t != "" {
			on := t == "on" || t == "true" || t == "1"
			a.limits.LeanPrompt = &on
		}
	}
	if v, ok, err := store.GetConfig(ctx, memory.KeyAgentReasoningEffort); err == nil && ok {
		a.limits.ReasoningEffort = NormalizeReasoningEffort(v)
	}
}

// ResetHistory clears in-memory history and, if a memory store is configured, the DB history too.
func (a *Agent) ResetHistory() {
	a.histMu.Lock()
	a.history = []ollama.Message{}
	a.toolSeq = 0
	a.historyLoaded = false
	a.historyGen++
	a.histMu.Unlock()
	if a.memStore != nil {
		if err := a.memStore.ClearHistory(context.Background(), a.sessionID); err != nil {
			log.Printf("[agent] clear history: %v", err)
		}
	}
}

// SetSession switches the agent to a different session, resetting in-memory state.
// Model returns the currently selected chat model (telemetry).
func (a *Agent) Model() string { return a.model }

func (a *Agent) SetSession(sessionID, personality string) {
	a.sessionID = sessionID
	a.personality = personality
	a.loadProfile()
	a.histMu.Lock()
	a.history = []ollama.Message{}
	a.toolSeq = 0
	a.historyLoaded = false
	a.historyGen++
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

// compactLiveContextIfNeeded shrinks the LIVE in-memory history when it gets
// critically large, independent of and not synced with MaybeSummarize's
// DB-side long-term memory (see liveContextCharBudget's doc comment). Never
// touches tool-result CONTENT — never truncates an MCP or any other tool's
// payload, only decides whether an older message is kept or replaced by a
// short summary of the span it was part of. Only ever cuts at a "user"
// message boundary so an in-progress turn's own tool calls are never split
// from the assistant message that requested them — this also means it can
// never discard the CURRENT task's own working data, only completed older
// turns.
// effectiveHistoryBudget is the char cap live compaction enforces: the backend's
// own context-derived budget when it has one (Ollama, from num_ctx), otherwise
// the large configured default (OpenAI/Anthropic size their own context). This
// is what keeps a long session's assembled prompt inside a small-context local
// model without needlessly over-compacting a large-context backend.
func (a *Agent) effectiveHistoryBudget() int {
	if a.ollama != nil {
		if b := a.ollama.ContextBudgetChars(); b > 0 && b < liveContextCharBudget {
			return b
		}
	}
	return liveContextCharBudget
}

func (a *Agent) compactLiveContextIfNeeded(ctx context.Context, events chan<- Event) {
	budget := a.effectiveHistoryBudget()
	a.histMu.Lock()
	total := 0
	for _, m := range a.history {
		total += len(m.Content)
	}
	over := total > budget
	a.histMu.Unlock()
	if over {
		a.compactLiveContextTo(ctx, events, budget/2)
	}
}

// forceCompactLiveContext compacts even when the live history is nominally under
// budget — the recovery path when a turn came back truncated (empty output at
// done_reason=length): shrink toward half the effective budget to free
// generation room before retrying. A no-op when there's nothing older safe to
// drop (a single huge in-progress turn), in which case the caller surfaces the
// "start a new chat" notice.
func (a *Agent) forceCompactLiveContext(ctx context.Context, events chan<- Event) {
	a.compactLiveContextTo(ctx, events, a.effectiveHistoryBudget()/2)
}

// compactLiveContextTo replaces the oldest completed turns with a summary until
// the kept tail is under `target` chars, cutting only at a "user" boundary.
func (a *Agent) compactLiveContextTo(ctx context.Context, events chan<- Event, target int) {
	a.histMu.Lock()
	total := 0
	for _, m := range a.history {
		total += len(m.Content)
	}
	// Walk forward from the start until the KEPT tail is back under the target,
	// then advance to the next "user" boundary for safety.
	cut, kept := 0, total
	for cut < len(a.history) && kept > target {
		kept -= len(a.history[cut].Content)
		cut++
	}
	for cut < len(a.history) && a.history[cut].Role != "user" {
		cut++
	}
	if cut == 0 || cut >= len(a.history) {
		a.histMu.Unlock()
		return // nothing safe to drop (e.g. a single huge in-progress turn)
	}
	dropped := append([]ollama.Message(nil), a.history[:cut]...)
	tail := append([]ollama.Message(nil), a.history[cut:]...)
	genBefore := a.historyGen
	a.histMu.Unlock()

	summary := a.summarizeDroppedSpan(ctx, dropped)

	a.histMu.Lock()
	if a.historyGen != genBefore {
		// InjectNote/ResetHistory/SetSession ran while summarizeDroppedSpan was
		// in flight (deliberately unlocked, up to ~20s) — a.history has moved
		// on since dropped/tail were captured. Applying our stale view now
		// would silently discard whatever that concurrent call did (a note,
		// a "new chat", a session switch). Skip this pass; if still over
		// budget, the next iteration will recompute from the current state.
		a.histMu.Unlock()
		log.Printf("[agent] session=%s compaction aborted: history changed concurrently (gen %d -> %d)", a.sessionID, genBefore, a.historyGen)
		return
	}
	a.history = append([]ollama.Message{
		{Role: "user", Content: "[Context compacted automatically — summary of the earlier exchanges: " + summary + "]"},
	}, tail...)
	a.historyGen++
	a.histMu.Unlock()

	log.Printf("[agent] session=%s compacted live context: dropped %d messages, kept %d", a.sessionID, len(dropped), len(tail))
	events <- Event{Type: "progress", Content: fmt.Sprintf(
		"Context compacted (%d older messages summarized) to stay within the model's limits.", len(dropped))}
}

// summarizeDroppedSpan asks the model to concisely summarize a span of
// messages being dropped from the live context during compaction. Best
// effort: never propagates an error or blocks the turn indefinitely — falls
// back to a generic note so compaction can't itself become a source of
// failures. Runs silently (no streamed "stream" events) so it never appears
// as if the assistant said something in the visible transcript.
func (a *Agent) summarizeDroppedSpan(ctx context.Context, dropped []ollama.Message) string {
	const fallback = "earlier exchanges compacted — details lost to a summarization error"
	var sb strings.Builder
	for _, m := range dropped {
		if m.Content == "" {
			continue
		}
		fmt.Fprintf(&sb, "[%s] %s\n", m.Role, m.Content)
	}
	if sb.Len() == 0 {
		return fallback
	}

	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req := ollama.ChatRequest{
		Model: a.model,
		Messages: []ollama.Message{
			{Role: "system", Content: "Summarize the following conversation excerpt concisely (a few sentences), preserving key facts, decisions, and any file/data references. Do not add commentary or preamble."},
			{Role: "user", Content: sb.String()},
		},
		NoThinking: true,
		Options:    ollama.Options{NumPredict: 300},
	}
	ch := make(chan ollama.StreamEvent, 20)
	go func() {
		a.ollama.Chat(sctx, req, ch)
		close(ch)
	}()

	var out strings.Builder
	failed := false
	for ev := range ch {
		if ev.Err != nil {
			failed = true
			continue
		}
		out.WriteString(ev.Content)
	}
	if failed {
		return fallback
	}
	summary := strings.TrimSpace(stripThinkingBlocks(out.String()))
	if summary == "" {
		return fallback
	}
	return summary
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

// Announce-without-acting guard. Measured on qwen3.8 (session model-test,
// 2026-08-20): the model ends a response on a stated next step ("je corrige
// l'outil", "je crée le widget") with zero tool calls, expecting a further
// turn — but a no-tool-call response ends the turn, so the task dies on a
// promise. systemPromptActTurn forbids it; this nudge is the harness-side
// fallback, gated on the reply *ending* with first-person intent so turns
// that close on a genuine report pay nothing.
const maxIntentNudges = 2

const intentNudgeMsg = "Your previous reply announced an action but contained no tool calls, so nothing ran and nothing will: a reply without tool calls ends the turn. Do the work now — make the tool calls. If the task is actually complete, reply with a brief summary of what was done instead."

// announceTailRe matches first-person intent phrasings ("je corrige…", "I'll…")
// in the tail of a reply. French first (the fleet's working language), then
// English. Deliberately conservative: past-tense reports ("je viens de créer",
// "widget created") must not match.
var announceTailRe = regexp.MustCompile(`(?i)\b(je (vais|m'en occupe|m'y mets|m'attaque|continue|commence|reprends|relance|corrige|crée|génère|lance|passe|termine|finis|fais|remets|resserre|répare|modifie|change|déploie|installe|configure|vérifie|teste|récupère|télécharge|écris|prépare|construis|patche?)|on (va|s'y met)|maintenant je|i('ll| will| am going to|'m going to)|let me|now i|next i)\b`)

// replyTail returns the last 200 runes of a reply — enough to hold the closing
// sentence or two where the announcement pattern shows up, without letting an
// intent phrase quoted early in a long final report trigger the nudge.
func replyTail(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 200 {
		r = r[len(r)-200:]
	}
	return string(r)
}

// sanitizeForDB makes a string safe for Postgres TEXT columns: replaces
// invalid UTF-8 sequences (e.g. raw gzip bytes leaked into a tool result)
// and strips NUL bytes, both of which Postgres rejects at insert time.
func sanitizeForDB(s string) string {
	if utf8.ValidString(s) && !strings.ContainsRune(s, 0) {
		return s
	}
	s = strings.ToValidUTF8(s, "�")
	return strings.ReplaceAll(s, "\x00", "")
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
	if err := a.memStore.AppendMessage(ctx, a.sessionID, msg.Role, sanitizeForDB(msg.Content), toolCallsJSON); err != nil {
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
	// Layered personality: base (default) + this session's own adaptation.
	persona := a.basePersonality
	if a.personality != "" {
		if persona != "" {
			persona += "\n\n# Workspace-specific adaptation\n"
		}
		persona += a.personality
	}

	if a.agentName != "" {
		sb.WriteString("Your name is ")
		sb.WriteString(a.agentName)
		sb.WriteString(".\n\n")
	}
	// Every place in this prompt that says "session=SESSION_ID" (widget JS, cron
	// scripts, /api/notes etc.) means THIS value, verbatim — never invent one, and
	// never reuse a value from a past turn or a different board.
	if a.sessionID != "" {
		sb.WriteString("Your session id is `")
		sb.WriteString(a.sessionID)
		sb.WriteString("`. Wherever these instructions say SESSION_ID, use this exact string.\n\n")
	}
	// The role always comes first, whatever the persona says — see systemPromptRole.
	// A persona describes how the agent talks; it must not be able to remove what it is.
	sb.WriteString(systemPromptRole)
	if strings.TrimSpace(persona) != "" {
		sb.WriteString("\n\n")
		sb.WriteString(persona)
	}
	lean := a.leanPrompt()
	sb.WriteString(systemPromptCoreFor(lean))
	if lean {
		sb.WriteString(systemPromptRetryLean)
	} else {
		sb.WriteString(systemPromptRetryGuided)
	}
	sb.WriteString(systemPromptCoreTailFor(lean))

	// Channel guidance: the "telegram" session is the user texting from their phone.
	if a.sessionID == telegramSessionID {
		sb.WriteString("\n\n## Channel: Telegram\nYou are texting the user on Telegram (their phone), not the dashboard. Reply like a text message: short and conversational, usually one or two sentences, no headings or long recaps. A simple \"thanks\" just needs a brief \"you're welcome\" — do not re-check state, recap, or re-run any task. Only perform actions or use tools when the user clearly asks for something new in their latest message; otherwise just reply in words.")
	}

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

	// Inject saved-skills index
	if a.skillsCtxFn != nil {
		if extra := a.skillsCtxFn(); extra != "" {
			sb.WriteString("\n\n")
			sb.WriteString(extra)
		}
	}

	// Inject the live index of services the agent has deployed.
	if a.servicesCtxFn != nil {
		if extra := a.servicesCtxFn(); extra != "" {
			sb.WriteString("\n\n")
			sb.WriteString(extra)
		}
	}

	// Inject what the user is currently looking at (context-aware chat).
	if a.viewCtxFn != nil {
		if v := a.viewCtxFn(); v != "" {
			sb.WriteString("\n\n## What the user is looking at right now\n")
			sb.WriteString(v)
			sb.WriteString("\nWhen the user says \"this\", \"it\", \"this email/note/event/task\", assume they mean what they are looking at above; act on it directly using the ids given (no need to ask which one).")
			sb.WriteString("\nYou are the glue between the apps: freely turn one thing into another when asked — an email into a task or calendar event, a task or note into a dashboard widget, etc. Use the relevant tools in sequence (e.g. read the email by UID, then create the task). When it's clearly useful, briefly offer such a bridge yourself (\"Want me to add this to your calendar?\"), but don't act without being asked.")
		}
	}

	// Global assistant: overview of every workspace (super-agent tier).
	if a.globalCtxFn != nil {
		if v := a.globalCtxFn(); v != "" {
			sb.WriteString("\n\n## You are the global assistant\n")
			sb.WriteString("You see and act across the user's entire space — all workspaces, plus mail, calendar, notes and tasks. The user's workspaces:\n")
			sb.WriteString(v)
		}
	}

	// Inject current date/time so the model can reason about time.
	// Explicit instruction: never output the date/time in responses.
	fmt.Fprintf(&sb, "\n\nCurrent date and time: %s. Current session ID: `%s`. Use these only as internal context — never write them in your responses. When generating widget code that calls /api/tool/ or /api/notify, always append ?session=%s to the URL.", time.Now().In(agentLocation).Format("2006-01-02 15:04"), a.sessionID, a.sessionID)

	// Grounding rule, near the end on purpose: late-prompt instructions are the
	// ones this size of model actually follows (see systemPromptRole's measurements).
	sb.WriteString(systemPromptGrounding)
	sb.WriteString(systemPromptDeliverable)
	if lean {
		// A capable model over-delivers; the small-model act-turn crutch is
		// replaced by the opposite discipline.
		sb.WriteString(systemPromptKeepItSimple)
	} else {
		sb.WriteString(systemPromptActTurn)
	}

	// Channel layer (Vortex): the phone constrains the *form* of the answer, not
	// who the agent is. Kept last so it wins over anything the personality says
	// about formatting. Everything here is read aloud by a TTS.
	if a.channel == voiceChannel {
		sb.WriteString(`

## You are on a phone call — speak, don't write
Your reply is read aloud by a speech synthesiser to someone holding a phone. Therefore:
- Answer in spoken French, in SHORT sentences. Be brief: a caller cannot skim.
- NO markdown, NO emoji, NO bullet lists, NO headings, NO code blocks, NO URLs.
- Never write symbols meant to be seen (*, #, backticks, arrows) — they get spoken.
- Give at most one or two key points, then stop. Ask one question at a time.
- Spell out nothing and never dictate long identifiers or links; offer to send them instead.
- If you need to think, do it briefly — the caller is waiting in silence.

You control the call through tools — the words alone do nothing:
- To hang up: when the caller has clearly finished (says goodbye, "c'est tout merci", "au revoir"), say a short farewell AND call end_call in the same turn. Saying goodbye without calling end_call leaves the line open.
- To transfer: when the caller asks to reach a person, call transfer_call (never just say you are transferring).
- To take a message: when the caller wants to leave one, or the person is unavailable, call take_message.

For ANY factual question (opening hours, prices, services, procedures, addresses…), you MUST call rag_search FIRST and answer only from what it returns — never from memory, never invent. If it returns nothing relevant, say plainly that you don't have that information and offer to take a message.`)
	}

	return sb.String()
}

// Chat processes a user message (with optional images) and streams events.
// images is a slice of base64-encoded image strings (raw base64, no data-URL prefix).
func (a *Agent) Chat(ctx context.Context, userMsg string, images []string, events chan<- Event) {
	// Snapshot the workspace into git once this turn ends, on every exit path.
	// Fire-and-forget on a background context so a cancelled/disconnected request
	// still commits, and so it never adds latency to the reply. CommitWorkspace is
	// serialized and fail-safe — it can never break the turn.
	defer func() { go a.executor.CommitWorkspace(context.Background(), userMsg) }()

	// Re-read the agent's name and base persona once per turn. They live in the DB and
	// are edited in Settings while this agent is connected, so loading them only at
	// construction made an edit invisible until the page was reloaded — and "new chat"
	// doesn't help: it clears the history but keeps the same Agent. Two small SELECTs
	// against an LLM call is free.
	a.loadProfile()

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

	// Resolved once per turn: a Settings change lands on the next message,
	// never mid-turn.
	maxIterations, thinking := a.effectiveLimits()
	a.turnThinking = thinking
	a.turnReasoningEffort = a.reasoningEffort()

	var emptyRetried bool
	intentNudges := 0
	// True once the user rejected a tool call this turn. The model then ends its
	// reply on "what would you like instead?" — which the intent-nudge heuristic
	// reads as an unfulfilled announcement and would push it to retry the very
	// call that was just rejected (measured on the first live test). A turn with
	// a rejection is allowed to end on a question.
	toolRejected := false
	for iter := 0; iter < maxIterations; iter++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Checked every iteration, not just at turn-end, so it also catches
		// bloat accumulating within one long multi-tool-call turn.
		a.compactLiveContextIfNeeded(ctx, events)

		fullContent, toolCalls, doneReason, err := a.callOllama(ctx, learningsCtx, events)
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

		// Empty response: nothing usable came back. WHY it's empty decides the fix.
		if strings.TrimSpace(fullContent) == "" && len(toolCalls) == 0 {
			// Truncation (done_reason=length/max_tokens): the model was cut off at
			// the token cap with nothing to show. TWO very different causes, told
			// apart by how big the live context actually is:
			//   - context genuinely huge → the prompt crowds out generation room →
			//     compacting frees room, and "start a new chat" is honest advice.
			//   - context small → the model spent its whole generation budget in
			//     the reasoning channel and never answered (heavy reasoner). This
			//     is NOT a context problem; compacting/new-chat won't help — the
			//     lever is capping reasoning_effort (see openai.reasoningEffort).
			// Reporting "context too long" when it's 6k tokens is a false diagnosis.
			if isTruncation(doneReason) {
				a.histMu.Lock()
				histChars := 0
				for _, m := range a.history {
					histChars += len(m.Content)
				}
				a.histMu.Unlock()
				contextBound := histChars > a.effectiveHistoryBudget()/2
				if !emptyRetried {
					log.Printf("[agent] truncated empty response (done_reason=%q, hist=%dc, context-bound=%v) — retrying", doneReason, histChars, contextBound)
					emptyRetried = true
					if contextBound {
						a.forceCompactLiveContext(ctx, events)
					}
					continue
				}
				log.Printf("[agent] truncated empty response again — stopping (context-bound=%v)", contextBound)
				msg := "\n\n_(Réponse interrompue : le modèle a épuisé son budget de génération en raisonnement sans produire de réponse. Reformule plus simplement, ou découpe la demande.)_"
				if contextBound {
					msg = "\n\n_(Réponse interrompue : le contexte de cette session est trop long pour le modèle. Démarre un nouveau chat pour repartir sur une base propre.)_"
				}
				events <- Event{Type: "stream", Content: msg}
				events <- Event{Type: "stream_end"}
				return
			}
			// Genuine empty (clean stop with no content): the model chose silence.
			// A single "continue" nudge is the right prod here.
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
			// Reply ends on an announced action with nothing to run it: nudge
			// the model to act instead of ending the turn (see announceTailRe).
			if !toolRejected && intentNudges < maxIntentNudges && announceTailRe.MatchString(replyTail(stripThinkingBlocks(fullContent))) {
				intentNudges++
				log.Printf("[agent] reply ends on an announcement with no tool calls — nudging to act (%d/%d)", intentNudges, maxIntentNudges)
				a.histMu.Lock()
				a.history = append(a.history, ollama.Message{Role: "user", Content: intentNudgeMsg})
				a.histMu.Unlock()
				continue
			}
			events <- Event{Type: "stream_end"}
			// Trigger summarization asynchronously after the turn completes
			if a.memStore != nil {
				go a.memStore.MaybeSummarize(context.Background(), a.sessionID, a.ollama, a.model, maxHistoryMessages, keepRecentMessages)
			}
			return
		}

		// Execute tools; each result becomes a "tool" message in history
		for _, tc := range toolCalls {
			// Some backends leak template markers into the function name (measured:
			// gpt-oss harmony via vLLM's openai parser emits "widget<|channel|>commentary").
			// The pollution is deterministic, so without this the model retries the
			// same unknown tool until the loop detector kills the turn.
			if i := strings.Index(tc.Function.Name, "<|"); i >= 0 {
				tc.Function.Name = strings.TrimSpace(tc.Function.Name[:i])
			}
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
			toolFailed := false
			rejected := false
			// Manual approval gate. Voice bypasses it — a caller can't click, and
			// silence while the dashboard waits would read as a dead line.
			if a.channel != voiceChannel && a.approvalNeededFn != nil && a.approvalNeededFn() && a.awaitApprovalFn != nil {
				events <- Event{Type: "approval_request", ID: toolID, Tool: tc.Function.Name, Input: tc.Function.Arguments}
				rejected = !a.awaitApprovalFn(ctx, toolID)
			}
			if rejected {
				toolRejected = true
				result = "Tool call rejected by the user. Do not retry it as-is — ask what they want done differently."
				toolFailed = true
			} else if tc.Function.Name == "update_system_prompt" {
				// Route the special-cased tool through the same policy checks as
				// every other tool, or a group with update_system_prompt disabled
				// (or a Webex sender without it) could still call it.
				if err := a.executor.Authorize(tc.Function.Name, tc.Function.Arguments); err != nil {
					result = fmt.Sprintf("Error: %v", err)
					toolFailed = true
				} else {
					result = a.handleUpdateSystemPrompt(ctx, tc.Function.Arguments)
				}
			} else {
				var execErr error
				result, toolImages, execErr = a.executor.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
				if execErr != nil {
					result = fmt.Sprintf("Error: %v", execErr)
					toolFailed = true
				}
				a.emitToolSideEffects(tc.Function.Name, tc.Function.Arguments, events)
			}

			events <- Event{
				Type:    "tool_result",
				ID:      toolID,
				Output:  result,
				Images:  toolImages,
				IsError: toolFailed,
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

	// Reached the budget, not necessarily a loop (identical-call loops are
	// caught above). Say where the knob is instead of blaming the chat.
	events <- Event{Type: "error", Content: fmt.Sprintf("Iteration limit reached (%d model calls this turn). Raise it in Settings › Agent, or send a follow-up to continue.", maxIterations)}
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

// extractScreenshotPaths returns the on-disk paths of the screenshots referenced
// in a browser_act result, so a vision captioner can describe them for a
// text-only chat model.
func extractScreenshotPaths(result, workspaceDir string) []string {
	var actions []struct {
		Action string `json:"action"`
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal([]byte(result), &actions); err != nil {
		return nil
	}
	var paths []string
	for _, a := range actions {
		if a.Action != "screenshot" || a.Status != "ok" || a.URL == "" {
			continue
		}
		fname := strings.TrimPrefix(a.URL, "/screenshots/")
		paths = append(paths, filepath.Join(workspaceDir, ".screenshots", fname))
	}
	return paths
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

func (a *Agent) callOllama(ctx context.Context, learningsCtx string, events chan<- Event) (string, []ollama.ToolCall, string, error) {
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
		// On the phone the caller waits in silence while the model reasons, so the
		// thinking budget is pure dead air. Turn it off for voice turns — and
		// whenever the user switched reasoning off in Settings.
		NoThinking:      a.channel == voiceChannel || !a.turnThinking,
		ReasoningEffort: a.turnReasoningEffort,
	}
	// num_ctx is filled in by the Ollama client (ollama.NumCtx); the OpenAI and
	// Anthropic backends ignore it and size their own context.

	log.Printf("[agent] → ollama: %d messages, %d tools, prompt_len=%d", len(messages), len(tools), len(prompt))

	ch := make(chan ollama.StreamEvent, 100)
	go func() {
		a.ollama.Chat(ctx, req, ch)
		close(ch)
	}()

	var contentBuilder strings.Builder
	var toolCalls []ollama.ToolCall
	var inThinking bool
	var doneReason string

	for ev := range ch {
		if ev.Err != nil {
			return contentBuilder.String(), nil, "", ev.Err
		}
		if ev.DoneReason != "" {
			doneReason = ev.DoneReason
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
	log.Printf("[agent] iter response: content=%q tool_calls=%d done_reason=%q", truncate(content, 120), len(toolCalls), doneReason)
	for i, tc := range toolCalls {
		log.Printf("[agent]   tool[%d] %s %s", i, tc.Function.Name, truncate(string(tc.Function.Arguments), 200))
	}
	return content, toolCalls, doneReason, nil
}

// isTruncation reports whether a finish reason means the model was cut off at
// the token cap / context edge (as opposed to a clean stop). Spans the three
// backends' vocabularies: Ollama "length", OpenAI "length", Anthropic "max_tokens".
func isTruncation(doneReason string) bool {
	return doneReason == "length" || doneReason == "max_tokens"
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
