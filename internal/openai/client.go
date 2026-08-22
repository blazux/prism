// Package openai implements an OpenAI-compatible chat backend, usable against
// SGLang, vLLM, TGI, LM Studio, llama.cpp, OpenAI or OpenRouter. It satisfies
// ollama.Backend by translating PRISM's wire-neutral ollama.* types to and from
// the OpenAI /v1 wire format (SSE streaming, fragmented tool-call deltas,
// reasoning_content). PRISM keeps the ollama.* types as the canonical pivot.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"prism/internal/ollama"
)

// defaultMaxTokens caps generation for one turn when the caller asks for no
// specific ceiling. It has to clear the longest legitimate answer — a whole
// widget, a long note — while still cutting off a model that has stopped
// converging. vLLM's own default is max_model_len minus the prompt (~242k on
// fleet-qwen35b), i.e. over an hour of streaming before the request ends by
// itself; hitting this cap instead ends the turn with finish_reason "length",
// which surfaces as a truncated answer rather than a dead chat.
const defaultMaxTokens = 16384

// reasoningEffort is sent as `reasoning_effort` on every request. gpt-oss (and
// other harmony/o-series-style models) otherwise default to heavy reasoning and
// can burn the ENTIRE max_tokens budget in the reasoning channel without ever
// emitting a final message — the request comes back with finish_reason "length"
// and empty content, which reads as a dead turn (measured 2026-08-22: gpt-oss
// on a 6k-token prompt, so NOT a context problem — pure reasoning overflow).
// "medium" makes it reason briefly (~500 chars) and answer within ~1.3k tokens.
// Empty or "none" omits the field (for backends that reject it; LiteLLM's
// drop_params also drops it harmlessly for models that don't know it).
var reasoningEffort = func() string {
	if v, ok := os.LookupEnv("OPENAI_REASONING_EFFORT"); ok {
		if v == "none" {
			return ""
		}
		return v
	}
	return "medium"
}()

// No penalty is applied by default, and this is not an oversight.
//
// A presence_penalty of 0.3 was tried against fleet-qwen35b (vLLM, GB10/sm_121)
// on 2026-07-16 and killed the engine on the first request carrying it:
// apply_penalties → "CUDA error: device-side assert triggered" → EngineDeadError,
// taking the whole server down. The penalty kernel is only reached when a penalty
// is non-zero, which is why that box served for 20h without touching it. Sending
// one by default would therefore kill the model on every chat turn.
//
// Options.PresencePenalty still exists for callers on a backend known to handle
// it (Ollama does). Do not wire a default here without re-testing on the box that
// will actually serve it. The max-token cap is the loop backstop instead.

// responseHeaderTimeout bounds the wait for the response head only, never the
// body — an SSE stream may then run as long as it likes. It covers the window
// where the prompt is queued and prefilled, so an upstream that accepts the
// connection and goes quiet fails loudly instead of hanging forever.
const responseHeaderTimeout = 3 * time.Minute

type Client struct {
	baseURL    string // includes the /v1 suffix, e.g. http://host:30000/v1
	apiKey     string // optional; local SGLang/vLLM usually need none
	httpClient *http.Client
}

// NewClient builds an OpenAI-compatible client. baseURL should point at the /v1
// root (a trailing slash is tolerated). apiKey may be empty.
func NewClient(baseURL, apiKey string) *Client {
	// Cloned from the default transport to keep ProxyFromEnvironment: deployments
	// behind a corporate proxy reach the gateway through it.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = responseHeaderTimeout
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		// No client-wide timeout: it would also cap the streamed body.
		httpClient: &http.Client{Timeout: 0, Transport: tr},
	}
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// ---- request translation -------------------------------------------------

type chatRequest struct {
	Model       string     `json:"model"`
	Messages    []chatMsg  `json:"messages"`
	Tools       []chatTool `json:"tools,omitempty"`
	Stream      bool       `json:"stream"`
	Temperature float64    `json:"temperature,omitempty"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	// Pointer so an explicit 0 (penalty off) still reaches the backend instead of
	// being dropped by omitempty and silently replaced by the default.
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`
	// Qwen/SGLang convention for turning extended reasoning off. Passes through
	// LiteLLM unharmed; ignored by backends that don't know it.
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
	// gpt-oss/o-series reasoning budget ("low"|"medium"|"high"). See reasoningEffort.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type chatMsg struct {
	Role       string         `json:"role"`
	Content    interface{}    `json:"content,omitempty"` // string, or []part for images
	ToolCalls  []respToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string              `json:"type"`
	Function ollama.ToolFunction `json:"function"`
}

type contentPart struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	ImageURL *imageURLField `json:"image_url,omitempty"`
}

type imageURLField struct {
	URL string `json:"url"`
}

// buildMessages converts the pivot history to OpenAI shape. OpenAI strictly
// requires that every assistant tool_call carries an id and every subsequent
// tool result references it via tool_call_id. PRISM's history doesn't track
// ids, so we synthesise them here: assistant tool_calls get sequential ids and
// each following tool message is matched, in order, to the pending ids.
func buildMessages(in []ollama.Message) []chatMsg {
	out := make([]chatMsg, 0, len(in))
	var pending []string // tool_call ids awaiting a tool result, FIFO
	seq := 0

	for _, m := range in {
		switch m.Role {
		case "assistant":
			cm := chatMsg{Role: "assistant"}
			if m.Content != "" {
				cm.Content = m.Content
			}
			for _, tc := range m.ToolCalls {
				seq++
				// Mistral (mistral-common) requires tool_call ids to match
				// [a-zA-Z0-9] with an exact length of 9 — "call_1" is rejected.
				// 9 zero-padded digits satisfies every backend's format.
				id := fmt.Sprintf("%09d", seq)
				pending = append(pending, id)
				cm.ToolCalls = append(cm.ToolCalls, respToolCall{
					ID:   id,
					Type: "function",
					Function: respToolCallFn{
						Name:      tc.Function.Name,
						Arguments: string(tc.Function.Arguments),
					},
				})
			}
			out = append(out, cm)
		case "tool":
			if len(pending) == 0 {
				// Orphaned tool result — its originating assistant tool_call was
				// summarized or truncated out of the replayed history. A tool
				// message with no matching tool_call id is rejected outright by
				// strict servers (mistral-common: "tool_call_id must be provided
				// for tool messages"), so drop it rather than emit a dangling one.
				// The summary already carries that older context.
				continue
			}
			id := pending[0]
			pending = pending[1:]
			out = append(out, chatMsg{Role: "tool", Content: m.Content, ToolCallID: id})
		default: // system, user
			if len(m.Images) > 0 {
				parts := []contentPart{{Type: "text", Text: m.Content}}
				for _, img := range m.Images {
					parts = append(parts, contentPart{
						Type:     "image_url",
						ImageURL: &imageURLField{URL: "data:image/jpeg;base64," + img},
					})
				}
				out = append(out, chatMsg{Role: m.Role, Content: parts})
			} else {
				out = append(out, chatMsg{Role: m.Role, Content: m.Content})
			}
		}
	}
	return out
}

// ---- streaming response --------------------------------------------------

type respToolCall struct {
	Index    int            `json:"index,omitempty"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function respToolCallFn `json:"function"`
}

type respToolCallFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			Reasoning        string         `json:"reasoning"` // some vLLM builds (Qwen3.5+DFlash) emit "reasoning" instead
			ToolCalls        []respToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// toolAccumulator reassembles tool-call deltas that arrive fragmented across
// SSE chunks, keyed by their stream index.
type toolAccumulator struct {
	order []int
	byIdx map[int]*respToolCallFn
}

func newToolAccumulator() *toolAccumulator {
	return &toolAccumulator{byIdx: map[int]*respToolCallFn{}}
}

func (t *toolAccumulator) add(deltas []respToolCall) {
	for _, d := range deltas {
		acc, ok := t.byIdx[d.Index]
		if !ok {
			acc = &respToolCallFn{}
			t.byIdx[d.Index] = acc
			t.order = append(t.order, d.Index)
		}
		if d.Function.Name != "" {
			acc.Name = d.Function.Name
		}
		acc.Arguments += d.Function.Arguments
	}
}

func (t *toolAccumulator) result() []ollama.ToolCall {
	if len(t.order) == 0 {
		return nil
	}
	calls := make([]ollama.ToolCall, 0, len(t.order))
	for _, idx := range t.order {
		acc := t.byIdx[idx]
		args := acc.Arguments
		if args == "" {
			args = "{}"
		} else if !json.Valid([]byte(args)) {
			// A dropped connection or a max_tokens cutoff mid-stream can leave the
			// accumulated arguments truncated. Storing that fragment verbatim would
			// poison the conversation history forever: every future turn replays it
			// to the backend, which rejects the whole request with a JSON parse
			// error on retry, indefinitely. Fall back to {} so the turn can proceed.
			snippet := args
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			log.Printf("[openai] tool call %q got invalid JSON arguments, discarding: %s", acc.Name, snippet)
			args = "{}"
		}
		calls = append(calls, ollama.ToolCall{
			Function: ollama.ToolCallFunction{
				Name:      acc.Name,
				Arguments: json.RawMessage(args),
			},
		})
	}
	return calls
}

// postChatWithRetry issues the streaming chat POST, retrying transient failures
// (connection errors like "no route to host"/refused/reset, or 502/503/504)
// with exponential backoff. It only retries the initial request — before any
// token is read — so nothing is ever streamed twice. This keeps a momentary
// network blip to the backend (common on a flaky link to a DGX Spark) from
// aborting a long agent turn. Non-transient statuses (4xx) and success are
// returned to the caller as-is; context cancellation stops retrying.
// retryBaseBackoff is the first retry delay (doubled each attempt); a package
// var so tests can shrink it.
var retryBaseBackoff = time.Second

func (c *Client) postChatWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	const maxAttempts = 4
	backoff := retryBaseBackoff
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		c.auth(req)

		resp, err := c.httpClient.Do(req)
		if err == nil {
			switch resp.StatusCode {
			case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
				// Backend up but not ready — treat as transient.
				b, _ := bufio.NewReader(resp.Body).ReadString('\x00')
				resp.Body.Close()
				lastErr = fmt.Errorf("openai backend returned %d: %s", resp.StatusCode, strings.TrimSpace(b))
			default:
				return resp, nil // success (200) or a non-transient status the caller handles
			}
		} else {
			lastErr = err // dial/connection failure
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

func (c *Client) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	payload := chatRequest{
		Model:       req.Model,
		Messages:    buildMessages(req.Messages),
		Stream:      true,
		Temperature: req.Options.Temperature,
		MaxTokens:   req.Options.NumPredict,
	}
	if payload.MaxTokens <= 0 {
		payload.MaxTokens = defaultMaxTokens
	}
	// Only ever sent when a caller explicitly asks for it — see the note above.
	if pp := req.Options.PresencePenalty; pp != 0 {
		payload.PresencePenalty = &pp
	}
	if req.NoThinking {
		payload.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
	} else if reasoningEffort != "" {
		// Cap reasoning so the model can't spend the whole generation budget
		// thinking and return empty content. Not set for voice/no-thinking turns.
		payload.ReasoningEffort = reasoningEffort
	}
	for _, t := range req.Tools {
		payload.Tools = append(payload.Tools, chatTool{Type: "function", Function: t.Function})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("marshal: %w", err)}
		return
	}

	resp, err := c.postChatWithRetry(ctx, body)
	if err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("http: %w", err)}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := bufio.NewReader(resp.Body).ReadString('\x00')
		out <- ollama.StreamEvent{Err: fmt.Errorf("openai backend returned %d: %s", resp.StatusCode, strings.TrimSpace(b))}
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	tools := newToolAccumulator()
	finish := ""

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			out <- ollama.StreamEvent{Err: ctx.Err()}
			return
		default:
		}

		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if string(data) == "[DONE]" {
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if think := ch.Delta.ReasoningContent; think != "" {
			out <- ollama.StreamEvent{Thinking: think}
		} else if ch.Delta.Reasoning != "" {
			out <- ollama.StreamEvent{Thinking: ch.Delta.Reasoning}
		}
		if ch.Delta.Content != "" {
			out <- ollama.StreamEvent{Content: ch.Delta.Content}
		}
		if len(ch.Delta.ToolCalls) > 0 {
			tools.add(ch.Delta.ToolCalls)
		}
		if ch.FinishReason != nil {
			finish = *ch.FinishReason
		}
	}

	if err := scanner.Err(); err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("scan: %w", err)}
		return
	}
	// "length" means the model was still going when it hit the cap: either a
	// genuinely huge answer, or a model looping instead of stopping. Both leave a
	// truncated turn behind, so say so rather than let it look like a clean stop.
	if finish == "length" {
		log.Printf("openai: generation hit the %d-token cap (truncated turn) — model %q", payload.MaxTokens, req.Model)
	}

	out <- ollama.StreamEvent{ToolCalls: tools.result(), Done: true, DoneReason: finish}
}

// ContextBudgetChars returns 0: an OpenAI-compatible server (vLLM/SGLang/LiteLLM)
// owns its own context sizing and truncates server-side, so the agent's live
// compaction need not enforce a token-derived cap on this backend's behalf.
func (c *Client) ContextBudgetChars() int { return 0 }

// ---- models / health -----------------------------------------------------

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	names := make([]string, len(result.Data))
	for i, m := range result.Data {
		names[i] = m.ID
	}
	return names, nil
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.ListModels(ctx)
	return err
}
