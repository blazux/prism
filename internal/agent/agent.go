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
	"prism/internal/timeprefs"
	"prism/internal/workspace"
)

type Event struct {
	Usage   *ModelUsage     `json:"usage,omitempty"`
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

// historyReplayMaxMessages caps how many stored rows a fresh Agent replays from
// conversation_history (see loadHistoryFromDB). A channel session (a Webex
// space, an in-app group room) is never reset and rebuilds its Agent on EVERY
// message, so an unbounded read meant each message paid for the entire history
// of the space — a cost that only ever grows. The character budget below is
// what actually decides how much reaches the model; this cap is what stops the
// read itself, and the compaction pass that follows it, from scaling with the
// age of the room. Override via HISTORY_REPLAY_MAX_MESSAGES.
var historyReplayMaxMessages = func() int {
	if v := os.Getenv("HISTORY_REPLAY_MAX_MESSAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 200
}()

// historyTruncatedNote stands in for the rows the replay cap left out. Honest
// like the compaction note: the model is told the details are gone rather than
// being left to assume it can see the whole conversation.
const historyTruncatedNote = "[Older messages in this conversation were not replayed — only the most recent ones are in your context. If something from earlier matters, ask instead of guessing.]"

// num_ctx now lives with the Ollama client (ollama.NumCtx), which both sets it
// on requests and derives the history budget from it via ContextBudgetChars —
// see effectiveHistoryBudget below.

type Agent struct {
	location        *time.Location
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
	approvalNeededFn  func() bool
	awaitApprovalFn   func(ctx context.Context, toolID string) bool
	prepareApprovalFn func(string)
	historyLoaded     bool // true after first DB load
	// historyGen counts every external mutation of history (InjectNote,
	// ResetHistory, SetSession) — bumped under histMu. compactLiveContextIfNeeded
	// captures it before releasing the lock for its ~20s best-effort LLM
	// summarization call, then checks it again before applying the compacted
	// result: if a concurrent InjectNote/ResetHistory/SetSession landed in
	// that window, the generation no longer matches and the stale compaction
	// is discarded instead of clobbering what changed in the meantime.
	historyGen int
	// compactMu serializes live-context compaction passes: a proactive
	// background pass (end of turn) and the next turn's foreground check must
	// never summarize the same span twice. Always taken BEFORE histMu, never
	// while holding it.
	compactMu sync.Mutex
	// channel is the surface this turn arrives from ("" = dashboard/browser,
	// "voice" = a phone call docked from Vox). It changes the *form* of the reply
	// (spoken, short, no markup) and disables extended reasoning — never the
	// agent's identity, which stays the same across channels.
	channel string
	// chatBlind is true when the selected conversation model cannot accept image
	// parts. Tool previews are captioned by the executor in this mode; user
	// attachments are kept as a textual notice instead of breaking the request.
	chatBlind bool
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
	LeanPrompt    *bool
	PromptProfile string
	// ReasoningEffort bounds the reasoning budget of thinking models
	// ("low"/"medium"/"high"/"xhigh"). "" = config / server default
	// (OPENAI_REASONING_EFFORT).
	ReasoningEffort string
	// HistoryBudgetChars caps how much conversation is replayed to the model on
	// every call, in characters. Only ever LOWERS the effective budget (see
	// effectiveHistoryBudget) — a channel agent must not be able to ask for
	// more context than the backend can take. 0 = deployment default.
	HistoryBudgetChars int
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

// SetChatBlind marks the selected chat model as text-only. This is separate
// from the executor's preview setting because user attachments are assembled
// by Agent.Chat itself.
func (a *Agent) SetChatBlind(blind bool) { a.chatBlind = blind }

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
// the budget: override → config → guided (false). Guided is the deliberate
// default: a Prism deployment normally runs a small local model (Ollama/vLLM),
// and lean is the marginal case a user opts into by hand.
func (a *Agent) promptProfile() string {
	if memory.ValidPromptProfile(a.limitsOverride.PromptProfile) {
		return a.limitsOverride.PromptProfile
	}
	if a.limitsOverride.LeanPrompt != nil {
		return memory.ResolvePromptProfile("", *a.limitsOverride.LeanPrompt)
	}
	if memory.ValidPromptProfile(a.limits.PromptProfile) {
		return a.limits.PromptProfile
	}
	return memory.ResolvePromptProfile("", a.limits.LeanPrompt != nil && *a.limits.LeanPrompt)
}
func (a *Agent) leanPrompt() bool { return a.promptProfile() != "guided" }

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
func (a *Agent) SetApprovalFns(needed func() bool, await func(context.Context, string) bool, prepare ...func(string)) {
	a.approvalNeededFn = needed
	a.awaitApprovalFn = await
	if len(prepare) > 0 {
		a.prepareApprovalFn = prepare[0]
	}
}

// voiceChannel is the phone surface (Prism Vox → Prism).
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
	msg.DBID = a.saveMessageToDB(context.Background(), msg)
	a.histMu.Lock()
	a.history = append(a.history, msg)
	a.historyGen++
	a.histMu.Unlock()
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
	all := append(nativeToolsFor(a.leanPrompt()), a.executor.AllDynamicTools()...)
	available := all[:0]
	for _, tool := range all {
		if a.executor.hiddenTools[tool.Function.Name] {
			continue
		}
		if tool.Function.Name == "subagent" && a.executor.serverTools["subagent"] == nil {
			continue
		}
		available = append(available, tool)
	}
	all = available
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
	a.location = time.Local
	if a.memStore == nil {
		return
	}
	store := a.memStore
	if m := userSessionPrefixRe.FindString(a.sessionID); m != "" && strings.HasPrefix(a.sessionID, m+"-") {
		store = store.ConfigScope(m)
	}
	ctx := context.Background()
	_, a.location = timeprefs.Read(ctx, store)
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
	a.limits.PromptProfile, _, _ = store.GetConfig(ctx, memory.KeyAgentPromptProfile)
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
func (a *Agent) ResetHistory() error {
	a.histMu.Lock()
	defer a.histMu.Unlock()
	if a.memStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.memStore.ClearHistory(ctx, a.sessionID); err != nil {
			return fmt.Errorf("clear history: %w", err)
		}
	}
	a.history = []ollama.Message{}
	a.toolSeq = 0
	a.historyLoaded = false
	a.historyGen++
	return nil

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

// clampHistoryTail trims a freshly loaded history tail down to maxRows, and
// reports whether anything was dropped.
//
// The cut never lands mid-turn: a "tool" row whose parent assistant tool_calls
// fell outside the window is a malformed sequence for the backends that
// validate the pairing, and an assistant reply without the question it answers
// is merely confusing. So the window is advanced to the first user row — the
// same boundary rule compactLiveContextTo cuts on. A window holding no user row
// at all is dropped entirely rather than replayed headless.
func clampHistoryTail(entries []memory.HistoryEntry, maxRows int) ([]memory.HistoryEntry, bool) {
	if maxRows <= 0 || len(entries) <= maxRows {
		return entries, false
	}
	entries = entries[len(entries)-maxRows:]
	cut := 0
	for cut < len(entries) && entries[cut].Role != "user" {
		cut++
	}
	return entries[cut:], true
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

	// Read one row beyond the cap so a truncation is detectable, and only the
	// tail — the head of a long-running space's history can never fit the
	// model's context anyway. See historyReplayMaxMessages.
	maxRows := historyReplayMaxMessages
	entries, err := a.memStore.LoadHistoryTail(ctx, a.sessionID, maxRows+1)
	if err != nil {
		log.Printf("[agent] load history: %v", err)
		return
	}
	// A previous Agent on this session may have compacted its live context
	// (see compactLiveContextTo). Replay the same state instead of the raw
	// history: the note stands in for every row before BeforeID, so a page
	// reload or a new connection doesn't hand the model a different (and
	// possibly over-budget) context than the one it was just working with.
	lc := a.memStore.GetLiveCompaction(ctx, a.sessionID)
	if lc != nil {
		kept := entries[:0]
		for _, e := range entries {
			if e.ID >= lc.BeforeID {
				kept = append(kept, e)
			}
		}
		entries = kept
	}
	// Apply the cap to what's left once the compaction note has taken its share.
	entries, truncated := clampHistoryTail(entries, maxRows)
	if lc != nil {
		a.history = append(a.history, ollama.Message{Role: "user", Content: lc.Summary})
	}
	if truncated {
		a.history = append(a.history, ollama.Message{Role: "user", Content: historyTruncatedNote})
	}
	for _, e := range entries {
		content := e.Content
		// Inject timestamp prefix on user messages only so the model can reason about time
		// (not on assistant messages, to avoid the model mimicking the pattern in its responses)
		if e.Role == "user" {
			content = fmt.Sprintf("[%s] %s", e.CreatedAt.In(a.timeLocation()).Format("2006-01-02 15:04"), e.Content)
		}
		msg := ollama.Message{Role: e.Role, Content: content, DBID: e.ID}
		if len(e.ToolCalls) > 0 && string(e.ToolCalls) != "null" {
			var tcs []ollama.ToolCall
			if json.Unmarshal(e.ToolCalls, &tcs) == nil {
				msg.ToolCalls = tcs
			}
		}
		a.history = append(a.history, msg)
	}
	switch {
	case lc != nil:
		log.Printf("[agent] session=%s loaded %d messages from DB (replayed live compaction note, rows before id %d covered by it, truncated=%v)", a.sessionID, len(a.history), lc.BeforeID, truncated)
	case truncated:
		log.Printf("[agent] session=%s loaded %d messages from DB (capped at the %d most recent rows)", a.sessionID, len(a.history), maxRows)
	default:
		log.Printf("[agent] session=%s loaded %d messages from DB", a.sessionID, len(a.history))
	}
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
	budget := liveContextCharBudget
	if a.ollama != nil {
		if b := a.ollama.ContextBudgetChars(); b > 0 && b < budget {
			budget = b
		}
	}
	// A caller-supplied budget only ever tightens: a chat channel wants a short
	// window for latency, but it can't hand the backend more than it accepts.
	if b := a.limitsOverride.HistoryBudgetChars; b > 0 && b < budget {
		budget = b
	}
	return budget
}

func (a *Agent) compactLiveContextIfNeeded(ctx context.Context, events chan<- Event) {
	budget := a.effectiveHistoryBudget()
	if a.liveHistoryChars() > budget {
		a.compactLiveContextTo(ctx, events, budget/2)
	}
}

// liveHistoryChars is the total content size of the live history.
func (a *Agent) liveHistoryChars() int {
	a.histMu.Lock()
	defer a.histMu.Unlock()
	total := 0
	for _, m := range a.history {
		total += len(m.Content)
	}
	return total
}

// liveContextSoftRatio is the fraction of the effective budget past which a
// turn's end triggers a PROACTIVE compaction in the background (see
// compactLiveContextProactively), well before the hard budget forces one in
// the foreground at the start of a turn — where the user waits through the
// summarization call with nothing but a progress line.
const liveContextSoftRatio = 0.7

// needsProactiveCompaction reports whether the live history has crossed the
// soft threshold.
func (a *Agent) needsProactiveCompaction() bool {
	return a.liveHistoryChars() > int(float64(a.effectiveHistoryBudget())*liveContextSoftRatio)
}

// compactLiveContextProactively is deferred by every turn: once the turn is
// over and the live history sits above the soft threshold, it compacts in the
// background, off the user's critical path. No events channel — the turn's
// channel is closed by the time this runs — so the outcome is logged only; the
// note the pass leaves in the history is what tells the model what happened.
// Safe against the next turn starting meanwhile: compactLiveContextTo splices
// against the current history, and a foreground pass simply waits on compactMu
// then finds nothing left to cut.
func (a *Agent) compactLiveContextProactively() {
	if !a.needsProactiveCompaction() {
		return
	}
	go a.compactLiveContextTo(context.Background(), nil, a.effectiveHistoryBudget()/2)
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
// events may be nil (background pass): then nothing is announced to the UI.
func (a *Agent) compactLiveContextTo(ctx context.Context, events chan<- Event, target int) {
	a.compactMu.Lock()
	defer a.compactMu.Unlock()

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
	genBefore := a.historyGen
	a.histMu.Unlock()

	summary, err := a.summarizeDroppedSpan(ctx, dropped)

	a.histMu.Lock()
	if a.historyGen != genBefore {
		// InjectNote/ResetHistory/SetSession ran while summarizeDroppedSpan was
		// in flight (deliberately unlocked, possibly for a couple of minutes) —
		// a.history has moved on since `dropped` was captured. Applying our
		// stale view now would silently discard whatever that concurrent call
		// did (a note, a "new chat", a session switch). Skip this pass; if
		// still over budget, the next check will recompute from the current
		// state.
		a.histMu.Unlock()
		log.Printf("[agent] session=%s compaction aborted: history changed concurrently (gen %d -> %d)", a.sessionID, genBefore, a.historyGen)
		return
	}
	// The note is honest either way: when the summary failed, the model is told
	// the details are gone rather than being handed a bland placeholder it
	// might mistake for the actual context.
	var note string
	if err == nil {
		note = "[Context compacted automatically — summary of the earlier exchanges: " + summary + "]"
	} else {
		note = "[Context compacted automatically — the earlier exchanges could NOT be summarized (" + err.Error() + "), so their details are lost. If something from before this point matters, ask the user instead of guessing.]"
	}
	// Splice against the CURRENT history, not a tail captured before the
	// summarization call: this may be a background pass and the next turn may
	// already have appended its own messages after the cut. Only gen-bumping
	// mutations change what precedes the cut; plain appends never do, so
	// history[:cut] is still exactly the span that was summarized.
	tail := a.history[cut:]
	beforeID := firstDBID(tail)
	if beforeID == 0 {
		if m := maxDBID(dropped); m > 0 {
			beforeID = m + 1
		}
	}
	a.history = append([]ollama.Message{{Role: "user", Content: note}}, tail...)
	a.historyGen++
	// Serialize persistence with ResetHistory, so an old background compaction
	// cannot be written back after the reset has cleared the database.
	a.persistLiveCompaction(note, beforeID)
	a.histMu.Unlock()

	if err == nil {
		log.Printf("[agent] session=%s compacted live context: dropped %d messages, kept %d (summary %d chars)", a.sessionID, len(dropped), len(tail), len(summary))
		if events != nil {
			events <- Event{Type: "progress", Content: fmt.Sprintf(
				"Context compacted (%d older messages summarized) to stay within the model's limits.", len(dropped))}
		}
		return
	}
	log.Printf("[agent] session=%s compacted live context WITHOUT a summary: dropped %d messages, kept %d — %v", a.sessionID, len(dropped), len(tail), err)
	if events != nil {
		events <- Event{Type: "progress", Content: fmt.Sprintf(
			"Context compacted (%d older messages dropped) but their summary failed (%v) — the assistant no longer has their details.", len(dropped), err)}
	}
}

// firstDBID returns the row id of the first persisted message in msgs (0 if none).
func firstDBID(msgs []ollama.Message) int64 {
	for _, m := range msgs {
		if m.DBID > 0 {
			return m.DBID
		}
	}
	return 0
}

// maxDBID returns the highest row id among msgs (0 if none is persisted).
func maxDBID(msgs []ollama.Message) int64 {
	var max int64
	for _, m := range msgs {
		if m.DBID > max {
			max = m.DBID
		}
	}
	return max
}

// persistLiveCompaction records the compaction note and its boundary so a fresh
// Agent on this session replays the same context (see loadHistoryFromDB).
// Best-effort: a failure only means a reload falls back to the raw history.
func (a *Agent) persistLiveCompaction(note string, beforeID int64) {
	if a.memStore == nil || beforeID == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.memStore.SetLiveCompaction(ctx, a.sessionID, memory.LiveCompaction{Summary: note, BeforeID: beforeID}); err != nil {
		log.Printf("[agent] session=%s persist live compaction: %v", a.sessionID, err)
	}
}

// Caps applied to each message when building the excerpt handed to the
// summarizer. Tool outputs are the bulk of a long session's history (file
// reads, HTTP bodies, search results) and the least useful to a summary —
// what matters is that the tool was called and what was concluded from it,
// which the assistant's own messages carry. Measured on Flash-Next: a raw
// 80k-char span is a ~40k-token cold prefill (~60s); trimmed, the same span is
// a few thousand tokens.
const (
	excerptToolChars = 500
	excerptMsgHead   = 3000
	excerptMsgTail   = 800
)

// compactionExcerpt renders the dropped span as a compact transcript for the
// summarizer.
func compactionExcerpt(dropped []ollama.Message) string {
	var sb strings.Builder
	for _, m := range dropped {
		content := m.Content
		if m.Role == "tool" {
			if len(content) > excerptToolChars {
				content = content[:excerptToolChars] + fmt.Sprintf(" …[tool output truncated, %d chars total]", len(m.Content))
			}
		} else if len(content) > excerptMsgHead+excerptMsgTail {
			content = content[:excerptMsgHead] + fmt.Sprintf(" …[%d chars truncated]… ", len(m.Content)-excerptMsgHead-excerptMsgTail) + content[len(content)-excerptMsgTail:]
		}
		if len(m.ToolCalls) > 0 {
			names := make([]string, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			content = strings.TrimSpace("(called tools: " + strings.Join(names, ", ") + ") " + content)
		}
		if content == "" {
			continue
		}
		fmt.Fprintf(&sb, "[%s] %s\n", m.Role, content)
	}
	return sb.String()
}

// summarizeTimeout sizes the summarizer's deadline to the excerpt: a fixed
// allowance for queueing and generation plus prefill time proportional to the
// input. Measured cold prefill on Flash-Next behind LiteLLM is ~0.7 ms/char
// (58s for 82k chars), so 1s per 1000 chars leaves margin; the previous fixed
// 20s deadline expired on every realistic span and the model never got a
// summary — only the fallback note.
func summarizeTimeout(excerptChars int) time.Duration {
	d := 30*time.Second + time.Duration(excerptChars/1000)*time.Second
	if d > 180*time.Second {
		d = 180 * time.Second
	}
	return d
}

// compactionSummaryPrompt asks for a HANDOFF, not a recap: what the assistant
// needs to pick the conversation back up as if nothing had been cut.
const compactionSummaryPrompt = `You are compacting the older part of a live conversation between a user and an AI assistant that uses tools. The original messages will be removed; write the handoff summary the assistant needs to continue seamlessly. Cover, in this order:
1. What the user is trying to achieve overall, and the task in progress right now (or the last one completed).
2. Requests or questions from the user that are still unanswered or pending.
3. Decisions taken, constraints, and preferences the user stated.
4. Files, paths, URLs, identifiers and key values that were read, created or modified, with their last known state.
5. Errors met and whether they were resolved.
Be factual and specific; keep names, paths and values verbatim. If the excerpt begins with an earlier compaction note, integrate it — it describes even older context. Write in the language the user writes in. Output only the summary: no preamble, no commentary, no markdown headings.`

// summarizeDroppedSpan asks the model to summarize a span of messages being
// dropped from the live context during compaction. Returns an error instead
// of a summary when the model could not produce one (timeout, backend error,
// empty output) so the caller can be honest about it — never blocks the turn
// past its sized deadline. Runs silently (no streamed "stream" events) so it
// never appears as if the assistant said something in the visible transcript.
func (a *Agent) summarizeDroppedSpan(ctx context.Context, dropped []ollama.Message) (string, error) {
	excerpt := compactionExcerpt(dropped)
	if excerpt == "" {
		return "", errors.New("nothing to summarize")
	}

	timeout := summarizeTimeout(len(excerpt))
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()

	req := ollama.ChatRequest{
		Model: a.model,
		Messages: []ollama.Message{
			{Role: "system", Content: compactionSummaryPrompt},
			{Role: "user", Content: excerpt},
		},
		NoThinking: true,
		Options:    ollama.Options{NumPredict: 1000},
	}
	ch := make(chan ollama.StreamEvent, 20)
	go func() {
		a.ollama.Chat(sctx, req, ch)
		close(ch)
	}()

	var out strings.Builder
	var firstErr error
	for ev := range ch {
		if ev.Err != nil {
			if firstErr == nil {
				firstErr = ev.Err
			}
			continue
		}
		out.WriteString(ev.Content)
	}
	if firstErr != nil {
		if sctx.Err() != nil {
			return "", fmt.Errorf("summarization timed out after %s (excerpt %d chars)", timeout, len(excerpt))
		}
		return "", fmt.Errorf("summarization failed after %s: %w", time.Since(started).Round(time.Second), firstErr)
	}
	summary := strings.TrimSpace(stripThinkingBlocks(out.String()))
	if summary == "" {
		return "", errors.New("model returned an empty summary")
	}
	return summary, nil
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
const maxIntentNudges = 1

const intentNudgeMsg = "Execution reminder: a reply without tool calls ends this turn. If you need clarification, authorization, or have already answered the request, stop and give that answer or question. Only if the user already requested an action and you have what you need, perform it with the relevant tool instead of merely announcing it."

// announceTailRe matches first-person intent phrasings ("je corrige…", "I'll…")
// in the tail of a reply. French first (the fleet's working language), then
// English. Deliberately conservative: past-tense reports ("je viens de créer",
// "widget created") must not match.
var announceTailRe = regexp.MustCompile(`(?i)\b(je (vais|m'en occupe|m'y mets|m'attaque|continue|commence|reprends|relance|corrige|crée|génère|lance|passe|termine|finis|fais|remets|resserre|répare|modifie|change|déploie|installe|configure|vérifie|teste|récupère|télécharge|écris|prépare|construis|patche?)|on (va|s'y met)|maintenant je|i('ll| will| am going to|'m going to)|let me|now i|next i)\b`)

// A conservative small-model fallback only. Never override a question or an
// explicit wait, including a clarification phrased without a question mark.
var waitingForUserRe = regexp.MustCompile(`(?i)(\b(wait|waiting|await|awaiting|until|unless|clarif|confirm|which|whether|please (provide|specify|choose|tell)|let me know|need (you|your))|j.attends|en attente|avant de|besoin de|précis|precis|confirme|dis.moi|dites.moi|indique|quel(le|s|les)?\b|si tu|si vous|une fois)`)

func shouldNudgeAnnouncement(profile, response string) bool {
	if profile != "guided" {
		return false
	}
	visible := stripThinkingBlocks(response)
	if strings.ContainsAny(visible, "?？") || waitingForUserRe.MatchString(visible) {
		return false
	}
	return announceTailRe.MatchString(replyTail(visible))
}

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

// saveMessageToDB persists a single message to the DB (best-effort) and
// returns its row id (0 when not persisted).
func (a *Agent) saveMessageToDB(ctx context.Context, msg ollama.Message) int64 {
	if a.memStore == nil {
		return 0
	}
	var toolCallsJSON json.RawMessage
	if len(msg.ToolCalls) > 0 {
		b, _ := json.Marshal(redactedToolCalls(msg.ToolCalls))
		toolCallsJSON = b
	}
	id, err := a.memStore.AppendMessage(ctx, a.sessionID, msg.Role, sanitizeForDB(msg.Content), toolCallsJSON)
	if err != nil {
		log.Printf("[agent] save message: %v", err)
		return 0
	}
	return id
}

// Personal timezone is reloaded each turn; TZ remains the deployment fallback.
func (a *Agent) timeLocation() *time.Location {
	if a.location != nil {
		return a.location
	}
	return time.Local
}

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
	sb.WriteString(systemPromptDecision)
	if strings.TrimSpace(persona) != "" {
		sb.WriteString("\n\n")
		sb.WriteString(persona)
	}
	lean := a.leanPrompt()
	minimal := a.promptProfile() == "minimal"
	if minimal {
		sb.WriteString(systemPromptMinimal)
		sb.WriteString(minimalSafetyPrompt())
	} else {
		sb.WriteString(systemPromptCoreFor(lean))
		if lean {
			sb.WriteString(systemPromptRetryLean)
		} else {
			sb.WriteString(systemPromptRetryGuided)
		}
		sb.WriteString(systemPromptCoreTailFor(lean))
	}

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
	if !minimal && a.servicesCtxFn != nil {
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
	fmt.Fprintf(&sb, "\n\nCurrent date and time: %s. Current session ID: `%s`. Use these only as internal context — never write them in your responses. Widget tool calls use prismTool(name,args), which handles session/auth automatically; do not append a session parameter to that helper.", time.Now().In(a.timeLocation()).Format("2006-01-02 15:04 MST -07:00")+" ("+a.timeLocation().String()+")", a.sessionID)

	// Grounding rule, near the end on purpose: late-prompt instructions are the
	// ones this size of model actually follows (see systemPromptRole's measurements).
	if !minimal {
		sb.WriteString(systemPromptGrounding)
		sb.WriteString(systemPromptDeliverable)
		if lean {
			// A capable model over-delivers; the small-model act-turn crutch is
			// replaced by the harness fact it cannot guess (a reply without a tool
			// call ends the turn) and by the opposite discipline.
			sb.WriteString(systemPromptTurnContract)
			sb.WriteString(systemPromptKeepItSimple)
		} else {
			sb.WriteString(systemPromptActTurn)
		}

	}

	// Channel layer: the phone constrains the *form* of the answer, not
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
	// Once this turn is over, compact the live context in the background if it
	// has grown past the soft threshold — so the NEXT turn doesn't start with a
	// forced foreground compaction the user has to sit through.
	defer a.compactLiveContextProactively()

	// Re-read the agent's name and base persona once per turn. They live in the DB and
	// are edited in Settings while this agent is connected, so loading them only at
	// construction made an edit invisible until the page was reloaded — and "new chat"
	// doesn't help: it clears the history but keeps the same Agent. Two small SELECTs
	// against an LLM call is free.
	a.loadProfile()

	// Load history from DB on first call in this session
	a.loadHistoryFromDB(ctx)

	// Text-only OpenAI-compatible models reject image parts with a hard 400. Keep
	// the turn usable: the user message remains in the conversation, but the
	// model receives a clear notice that it cannot inspect the attachment.
	if a.chatBlind && len(images) > 0 {
		userMsg += fmt.Sprintf("\n[The user attached %d image(s), but this chat model cannot inspect images.]", len(images))
		images = nil
	}

	// Prefix user message with timestamp so the model can reason about time.
	// Only user messages get the prefix — assistant messages don't, to avoid the model
	// mimicking the pattern and outputting timestamps in its own responses.
	timestampedContent := fmt.Sprintf("[%s] %s", time.Now().In(a.timeLocation()).Format("2006-01-02 15:04"), userMsg)
	userMessage := ollama.Message{Role: "user", Content: timestampedContent, Images: images}

	// For DB: store clean content (created_at column is the canonical timestamp).
	// Saved before the in-memory append so the live copy carries its row id.
	dbContent := userMsg
	if len(images) > 0 {
		dbContent += fmt.Sprintf(" [%d image(s) attached]", len(images))
	}
	userMessage.DBID = a.saveMessageToDB(ctx, ollama.Message{Role: "user", Content: dbContent})
	a.histMu.Lock()
	a.history = append(a.history, userMessage)
	historyLen := len(a.history)
	a.histMu.Unlock()

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
	ctx = withModelBudget(ctx, maxIterations)
	a.turnThinking = thinking
	a.turnReasoningEffort = a.reasoningEffort()

	var emptyRetried bool
	intentNudges := 0
	visionRetry := false
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
		if ctx.Err() != nil {
			events <- Event{Type: "stream_end"}
			return
		}
		if err != nil {
			// Some compatible gateways advertise no vision capability only when
			// they receive the first image. Recover once by replaying the request
			// without image parts and remember the capability for later turns.
			if !visionRetry && isVisionUnsupportedError(err) {
				visionRetry = true
				a.SetChatBlind(true)
				a.stripHistoryImages()
				log.Printf("[agent] chat backend rejected images; retrying without image parts: %v", err)
				continue
			}
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
		// Strip thinking blocks before saving to DB (not user-visible content)
		dbMsg := assistantMsg
		dbMsg.Content = stripThinkingBlocks(fullContent)
		assistantMsg.DBID = a.saveMessageToDB(ctx, dbMsg)
		a.histMu.Lock()
		a.history = append(a.history, assistantMsg)
		a.histMu.Unlock()

		if len(toolCalls) == 0 {
			// Reply ends on an announced action with nothing to run it: nudge
			// the model to act instead of ending the turn (see announceTailRe).
			if !toolRejected && intentNudges < maxIntentNudges && shouldNudgeAnnouncement(a.promptProfile(), fullContent) {
				intentNudges++
				log.Printf("[agent] reply ends on an announcement with no tool calls — nudging to act (%d/%d)", intentNudges, maxIntentNudges)
				a.histMu.Lock()
				a.history = append(a.history, ollama.Message{Role: "system", Content: intentNudgeMsg})
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
				Input: redactToolArgs(tc.Function.Arguments),
			}

			var result string
			var toolImages []string
			toolFailed := false
			rejected := false
			// Manual approval gate. Voice bypasses it — a caller can't click, and
			// silence while the dashboard waits would read as a dead line.
			if a.channel != voiceChannel && a.approvalNeededFn != nil && a.approvalNeededFn() && a.awaitApprovalFn != nil {
				if a.prepareApprovalFn != nil {
					a.prepareApprovalFn(toolID)
				}
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
			toolMsg.DBID = a.saveMessageToDB(ctx, toolMsg)
			a.histMu.Lock()
			a.history = append(a.history, toolMsg)
			a.histMu.Unlock()
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
		data, err := workspace.ReadFile(workspaceDir, filepath.Join(".screenshots", fname))
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

// stripHistoryImages removes image payloads after a backend proves it is text-only.
// Keep a short marker so the model knows an attachment existed and can ask the
// user for a description instead of hallucinating what it contained.
func (a *Agent) stripHistoryImages() {
	a.histMu.Lock()
	defer a.histMu.Unlock()
	for i := range a.history {
		if len(a.history[i].Images) == 0 {
			continue
		}
		count := len(a.history[i].Images)
		a.history[i].Images = nil
		marker := fmt.Sprintf("\n[Omitted %d image(s): this chat model cannot inspect images.]", count)
		if !strings.Contains(a.history[i].Content, "this chat model cannot inspect images") {
			a.history[i].Content += marker
		}
	}
}

func isVisionUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "at most 0 image") ||
		strings.Contains(msg, "does not support image") ||
		strings.Contains(msg, "image input is not supported") ||
		(strings.Contains(msg, "image") && strings.Contains(msg, "not supported"))
}

func (a *Agent) callOllama(ctx context.Context, learningsCtx string, events chan<- Event) (string, []ollama.ToolCall, string, error) {
	if err := consumeModelCall(ctx); err != nil {
		return "", nil, "", err
	}
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

	started := time.Now()
	var measured *ollama.Usage
	complete := false
	defer func() {
		raw, _ := json.Marshal(tools)
		record := &ModelUsage{Scope: "main_chat", Model: a.model, Profile: a.promptProfile(), DurationMS: time.Since(started).Milliseconds(), SystemBytes: len(prompt), ToolBytes: len(raw), MessageCount: len(messages), Complete: complete, Usage: measured}
		select {
		case events <- Event{Type: "model_usage", Usage: record}:
		case <-ctx.Done():
		}
		if a.memStore != nil {
			a.memStore.AddUsage(ctx, 0, a.sessionID, "model_request", a.model, 1, map[string]interface{}{"measurement": record})
		}
	}()
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
		if ev.Usage != nil {
			measured = ev.Usage
		}
		if ev.Done {
			complete = ev.DoneReason != "interrupted"
		}

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
		log.Printf("[agent]   tool[%d] %s %s", i, tc.Function.Name, truncate(string(redactToolArgs(tc.Function.Arguments)), 200))
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
	case "write_file", "edit_file":
		events <- Event{
			Type: "file_changed",
			Path: str("path"),
		}
	}
}

// SetBackend changes chat routing between turns. The caller must first cancel
// and await any active turn; conversation history and context hooks survive.
func (a *Agent) SetBackend(backend ollama.Backend, model string) {
	a.ollama = backend
	a.model = model
}

// ReloadStoredHistory is used by an idle browser agent after another tab completed
// a turn in the same conversation. It never deletes stored messages.
func (a *Agent) ReloadStoredHistory() {
	if a.memStore == nil {
		return
	}
	a.histMu.Lock()
	defer a.histMu.Unlock()
	a.history = nil
	a.historyLoaded = false
}
