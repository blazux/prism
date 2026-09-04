package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// retryBaseBackoff is the first retry delay (doubled each attempt); a package
// var so tests can shrink it. Mirrors internal/openai/client.go's
// postChatWithRetry — the GX10 fleet's flaky link bites this backend just as
// much as the OpenAI-compatible one, and until now only that one retried.
var retryBaseBackoff = time.Second

// postChat retries a connection failure or a transient 502/503/504 with
// exponential backoff (same transient-status set and budget as the
// OpenAI-compatible client) instead of failing an entire agent turn on one
// blip. Non-transient statuses and success are returned to the caller as-is;
// context cancellation stops retrying immediately.
func (c *Client) postChat(ctx context.Context, body []byte) (*http.Response, error) {
	const maxAttempts = 4
	backoff := retryBaseBackoff
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err == nil {
			switch resp.StatusCode {
			case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
				resp.Body.Close()
				lastErr = fmt.Errorf("ollama returned %d", resp.StatusCode)
			default:
				return resp, nil // success (200) or a non-transient status the caller handles
			}
		} else {
			lastErr = fmt.Errorf("http: %w", err) // dial/connection failure
		}

		if attempt == maxAttempts || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}

	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return nil, lastErr
}

// Backend is the wire-neutral contract every LLM provider must satisfy. The
// Ollama and OpenAI-compatible (SGLang/vLLM/…) clients both implement it, so
// callers depend on this interface rather than a concrete client.
type Backend interface {
	Chat(ctx context.Context, req ChatRequest, out chan<- StreamEvent)
	Ping(ctx context.Context) error
	ListModels(ctx context.Context) ([]string, error)
	// ContextBudgetChars reports how many chars of replayed history this backend
	// can safely hold, so live-context compaction fits the assembled prompt to
	// the backend's real context window. 0 means "no hard limit the caller must
	// enforce" — used by servers that own their own (large) context sizing, i.e.
	// the OpenAI-compatible and Anthropic backends.
	ContextBudgetChars() int
}

// DefaultNumPredict caps the tokens generated for one turn when the caller asks for
// no specific ceiling. Ollama's own default is -1: generate until the context is
// full. A model that loops instead of emitting a stop token then streams for as long
// as that takes, with the caller blocked and nothing to log — which is exactly how a
// chat ends up never answering. Generous enough for a whole widget or a long note;
// hitting it truncates the turn instead of hanging it.
const DefaultNumPredict = 16384

// NumCtx is the context window (tokens) this backend loads models at. Ollama
// otherwise defaults to 8192 for qwen3.8 — far too small once a long agentic
// session's prompt (system prompt + tool schemas + history) is assembled: the
// prompt crowds out the generation room, and a THINKING model spends what's
// left on reasoning tokens and emits an empty final message. 32768 leaves room
// to reason AND answer; override via OLLAMA_NUM_CTX.
var NumCtx = func() int {
	if v := os.Getenv("OLLAMA_NUM_CTX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 32768
}()

// contextReserveTokens is what NumCtx budgets for things that are NOT replayed
// history but still occupy the loaded context every turn: the generation window
// (thinking + answer) plus the freshly-assembled system prompt and tool schemas.
// ContextBudgetChars subtracts it so the compaction threshold keeps the whole
// assembled prompt inside NumCtx — the history budget is only what's left.
const contextReserveTokens = 18000 // ~8k generation + ~10k system prompt & tools

// charsPerToken is a rough conversion for the char-based size caps used
// throughout the agent (no tokenizer dependency anywhere). Mixed FR/EN/code
// runs ~3.5 chars/token; kept slightly low so the derived budget errs small.
const charsPerToken = 3.5

// responseHeaderTimeout bounds the wait for the response head only, never the body —
// a streamed answer may take as long as it likes. It covers the window where the
// prompt is queued and prefilled, so a server that accepts the connection and then
// goes quiet fails loudly instead of hanging forever.
const responseHeaderTimeout = 3 * time.Minute

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	// Cloned from the default transport to keep ProxyFromEnvironment.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = responseHeaderTimeout
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout:   0, // no client-wide timeout: it would also cap the streamed body
			Transport: tr,
		},
	}
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"` // extended thinking (Qwen3, QwQ, etc.)
	Images    []string   `json:"images,omitempty"`   // base64-encoded images for multimodal models
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  ToolParameters `json:"parameters"`
}

type ToolParameters struct {
	Type string `json:"type"`
	// omitempty so a no-parameter tool serializes as {"type":"object"} rather than
	// {"properties":null,"required":null} — strict OpenAI-compatible validators
	// (e.g. Mistral's mistral-common) reject the nulls with "None is not of type
	// 'array'/'object'". Lenient backends (Ollama, qwen parser) accept both.
	Properties map[string]ToolProperty `json:"properties,omitempty"`
	Required   []string                `json:"required,omitempty"`
}

type ToolProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	// Enum carries a parameter's allowed values through to the model. Omitted
	// when empty so unconstrained params serialize unchanged. Without it a tool
	// whose schema constrains a field (e.g. status ∈ {open,closed}) reaches the
	// model as a bare "string" and it guesses — a failed call the enum prevents.
	Enum []string `json:"enum,omitempty"`
	// Items describes an array parameter's element type. Without it an array
	// reaches the model as a bare "array" and it guesses whether the elements are
	// strings, numbers or objects — a failed call the item schema prevents.
	Items *ToolProperty `json:"items,omitempty"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
	Options  Options   `json:"options,omitempty"`
	// NoThinking asks the model to skip extended reasoning for this turn. Set on
	// the voice channel, where a caller waits in silence while the model thinks.
	// Wire-neutral (each backend translates it); Ollama's translation is the
	// Think field below, set by Client.Chat.
	NoThinking bool `json:"-"`
	// ReasoningEffort bounds how much a reasoning model thinks this turn
	// ("low"/"medium"/"high"/"xhigh" — the accepted set is model-specific).
	// Wire-neutral: the OpenAI backend sends it as reasoning_effort, Ollama has
	// no equivalent and ignores it. Empty = the backend's own default.
	ReasoningEffort string `json:"-"`
	// Think is Ollama's own switch (/api/chat "think": false) for models with a
	// thinking mode (Qwen3, DeepSeek-R1, gpt-oss…). Omitted → the model's
	// default; never sent true so models without the mode don't reject it.
	Think *bool `json:"think,omitempty"`
}

type Options struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumCtx      int     `json:"num_ctx,omitempty"`
	// NumPredict caps the tokens generated for one turn. Left at 0 each backend
	// applies its own ceiling — for vLLM that is max_model_len minus the prompt,
	// so a model that loops instead of emitting a stop token keeps generating for
	// the better part of an hour with the caller blocked on the stream.
	NumPredict int `json:"num_predict,omitempty"`
	// PresencePenalty nudges the model away from tokens it has already emitted
	// this turn, which is what keeps a long conversation from collapsing into a
	// loop. It applies to the generated text only, never the prompt.
	PresencePenalty float64 `json:"presence_penalty,omitempty"`
}

type ChatChunk struct {
	Model      string  `json:"model"`
	Message    Message `json:"message"`
	Done       bool    `json:"done"`
	DoneReason string  `json:"done_reason"` // "stop" | "length" | …; "length" = truncated
}

type StreamEvent struct {
	Content   string
	Thinking  string
	ToolCalls []ToolCall
	Done      bool
	// DoneReason carries the backend's finish reason on the terminal event
	// ("length" = the model was cut off at the token cap / context edge, so an
	// empty result means truncation, not a chosen silence). Empty otherwise.
	DoneReason string
	Err        error
}

// ContextBudgetChars derives the safe history-content budget from NumCtx, minus
// the reserve for generation + system prompt + tool schemas (contextReserveTokens).
// This is what keeps a long session's assembled prompt inside the loaded context.
func (c *Client) ContextBudgetChars() int {
	usable := NumCtx - contextReserveTokens
	if usable < 4000 {
		usable = 4000 // never compact so hard the model can't see recent turns
	}
	return int(float64(usable) * charsPerToken)
}

func (c *Client) Chat(ctx context.Context, req ChatRequest, out chan<- StreamEvent) {
	req.Stream = true
	if req.Options.NumPredict == 0 {
		req.Options.NumPredict = DefaultNumPredict
	}
	if req.Options.NumCtx == 0 {
		req.Options.NumCtx = NumCtx
	}
	if req.NoThinking {
		off := false
		req.Think = &off
	}

	body, err := json.Marshal(req)
	if err != nil {
		out <- StreamEvent{Err: fmt.Errorf("marshal: %w", err)}
		return
	}

	resp, err := c.postChat(ctx, body)
	if err != nil {
		out <- StreamEvent{Err: err}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("[ollama] chat request failed: status=%d body=%s", resp.StatusCode, errBody)
		out <- StreamEvent{Err: fmt.Errorf("ollama returned %d: %s", resp.StatusCode, errBody)}
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			out <- StreamEvent{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chunk ChatChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}

		ev := StreamEvent{
			Content:    chunk.Message.Content,
			Thinking:   chunk.Message.Thinking,
			ToolCalls:  chunk.Message.ToolCalls,
			Done:       chunk.Done,
			DoneReason: chunk.DoneReason,
		}
		out <- ev

		if chunk.Done {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		out <- StreamEvent{Err: fmt.Errorf("scan: %w", err)}
	}
}

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	return names, nil
}
