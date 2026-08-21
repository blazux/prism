// Package anthropic implements a chat backend for Anthropic's Messages API,
// authenticated with a console API key. It satisfies ollama.Backend by
// translating PRISM's wire-neutral ollama.* types to and from the Messages wire
// format. PRISM keeps the ollama.* types as the canonical pivot.
//
// See credential.go for why a Claude Pro/Max subscription is not an option here.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"prism/internal/ollama"
)

// DefaultBaseURL is Anthropic's API root. Overridable so the backend can be
// pointed at a compatible endpoint (a proxy, a gateway) without code changes.
const DefaultBaseURL = "https://api.anthropic.com"

// apiVersion is required on every Messages request.
const apiVersion = "2023-06-01"

// defaultMaxTokens caps generation for one turn when the caller asks for no
// ceiling. max_tokens is mandatory on this API — unlike the OpenAI wire there is
// no "until the context runs out" default to inherit.
const defaultMaxTokens = 16384

// responseHeaderTimeout bounds the wait for the response head only, never the
// body — the SSE stream may then run as long as it likes.
const responseHeaderTimeout = 3 * time.Minute

// betas requested on every call. Fine-grained tool streaming is the only one
// that changes what this backend sees: tool arguments arrive as fragments
// without being buffered to completion first, which is what toolBuilder
// reassembles. It can leave the JSON truncated if the stream dies mid-call —
// toolBuilder handles that. GA on current models, kept for older ones that
// still gate on the header.
var betas = []string{"fine-grained-tool-streaming-2025-05-14"}

type Client struct {
	baseURL string
	apiKey  string
	// fallbackModel is offered by ListModels when Anthropic won't enumerate
	// models for this credential, so the picker still shows what is configured.
	fallbackModel string
	httpClient    *http.Client
}

// NewClient builds an Anthropic backend. baseURL may be empty for the public
// API. fallbackModel is the configured chat model, used when model enumeration
// is unavailable.
func NewClient(baseURL, apiKey, fallbackModel string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// Cloned from the default transport to keep ProxyFromEnvironment.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = responseHeaderTimeout
	return &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		apiKey:        strings.TrimSpace(apiKey),
		fallbackModel: fallbackModel,
		// No client-wide timeout: it would also cap the streamed body.
		httpClient: &http.Client{Timeout: 0, Transport: tr},
	}
}

// auth sets the credential and the headers every Messages request needs. It
// refuses a credential this backend cannot use rather than letting Anthropic
// answer with an opaque 401 — see ValidateKey.
func (c *Client) auth(req *http.Request) error {
	if err := ValidateKey(c.apiKey); err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("anthropic-beta", strings.Join(betas, ","))
	req.Header.Set("x-api-key", c.apiKey)
	return nil
}

// ---- request ------------------------------------------------------------

var retryBaseBackoff = time.Second

// postMessages issues the streaming POST, retrying transient failures with
// exponential backoff. Only the initial request is retried — before any token is
// read — so nothing is ever streamed twice.
//
// 429 is deliberately NOT retried. On a subscription it means the plan's usage
// window is exhausted, which no amount of backoff fixes; surfacing it lets the
// caller read Anthropic's own message instead of waiting out four pointless
// attempts.
func (c *Client) postMessages(ctx context.Context, body []byte) (*http.Response, error) {
	const maxAttempts = 4
	backoff := retryBaseBackoff
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if err := c.auth(req); err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err == nil {
			switch resp.StatusCode {
			// 529 is Anthropic's "overloaded", the one status that genuinely
			// clears on its own within seconds.
			case http.StatusInternalServerError, http.StatusBadGateway,
				http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
				b, _ := readLimited(resp.Body)
				resp.Body.Close()
				lastErr = fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
			default:
				return resp, nil
			}
		} else {
			lastErr = err
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
	if err := ValidateKey(c.apiKey); err != nil {
		out <- ollama.StreamEvent{Err: err}
		return
	}

	// Extended thinking is never requested. Anthropic signs each thinking block
	// against the turn content preceding it, and replaying a turn whose blocks
	// have been reordered or stripped fails with "thinking blocks in the latest
	// assistant message cannot be modified". PRISM's pivot stores thinking as
	// plain text with no signature, so it could not replay them faithfully —
	// asking for them would break the very next tool turn.
	payload := apiRequest{
		Model:     req.Model,
		System:    buildSystem(req.Messages),
		Messages:  buildMessages(req.Messages),
		Tools:     buildTools(req.Tools),
		MaxTokens: req.Options.NumPredict,
		Stream:    true,
	}
	if payload.MaxTokens <= 0 {
		payload.MaxTokens = defaultMaxTokens
	}
	if t := req.Options.Temperature; t > 0 {
		// The Messages API caps temperature at 1; the pivot allows Ollama's 0–2.
		if t > 1 {
			t = 1
		}
		payload.Temperature = &t
	}
	if len(payload.Messages) == 0 {
		out <- ollama.StreamEvent{Err: fmt.Errorf("anthropic: nothing to send (history has no user or assistant turn)")}
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("marshal: %w", err)}
		return
	}

	resp, err := c.postMessages(ctx, body)
	if err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("http: %w", err)}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := readLimited(resp.Body)
		log.Printf("[anthropic] chat request failed: status=%d body=%s", resp.StatusCode, b)
		out <- ollama.StreamEvent{Err: fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))}
		return
	}

	c.readStream(ctx, resp.Body, req.Model, payload.MaxTokens, out)
}

// ---- streaming response -------------------------------------------------

// streamEvent covers every server-sent event shape we act on. The event: line is
// ignored — the JSON payload names its own type.
type streamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`

	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`

	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`

	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// toolBuilder reassembles tool_use blocks: the name and id arrive with the
// block's start event, the arguments as a run of JSON fragments keyed by the
// block index.
type toolBuilder struct {
	order []int
	byIdx map[int]*pendingTool
}

type pendingTool struct {
	name string
	args strings.Builder
}

func newToolBuilder() *toolBuilder {
	return &toolBuilder{byIdx: map[int]*pendingTool{}}
}

func (t *toolBuilder) start(idx int, name string) {
	if _, ok := t.byIdx[idx]; !ok {
		t.order = append(t.order, idx)
	}
	t.byIdx[idx] = &pendingTool{name: name}
}

func (t *toolBuilder) addArgs(idx int, fragment string) {
	if p, ok := t.byIdx[idx]; ok {
		p.args.WriteString(fragment)
	}
}

func (t *toolBuilder) result() []ollama.ToolCall {
	if len(t.order) == 0 {
		return nil
	}
	calls := make([]ollama.ToolCall, 0, len(t.order))
	for _, idx := range t.order {
		p := t.byIdx[idx]
		args := p.args.String()
		if args == "" {
			// A tool called with no arguments streams no input_json_delta at all.
			args = "{}"
		} else if !json.Valid([]byte(args)) {
			// A dropped connection or a max_tokens cutoff mid-stream leaves the
			// fragments truncated. Storing that would poison the history: every
			// later turn would replay unparseable arguments.
			snippet := args
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			log.Printf("[anthropic] tool call %q got invalid JSON arguments, discarding: %s", p.name, snippet)
			args = "{}"
		}
		calls = append(calls, ollama.ToolCall{
			Function: ollama.ToolCallFunction{
				Name:      p.name,
				Arguments: json.RawMessage(args),
			},
		})
	}
	return calls
}

func (c *Client) readStream(ctx context.Context, body io.Reader, model string, maxTokens int, out chan<- ollama.StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	tools := newToolBuilder()
	stopReason := ""

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

		var ev streamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				tools.start(ev.Index, ev.ContentBlock.Name)
			}

		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					out <- ollama.StreamEvent{Content: ev.Delta.Text}
				}
			case "thinking_delta":
				if ev.Delta.Thinking != "" {
					out <- ollama.StreamEvent{Thinking: ev.Delta.Thinking}
				}
			case "input_json_delta":
				tools.addArgs(ev.Index, ev.Delta.PartialJSON)
			}

		case "message_delta":
			if ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}

		case "error":
			msg := ev.Error.Message
			if msg == "" {
				msg = string(data)
			}
			out <- ollama.StreamEvent{Err: fmt.Errorf("anthropic stream error (%s): %s", ev.Error.Type, msg)}
			return

		case "message_stop":
			// Anthropic closes the body right after; stop reading rather than
			// wait on a scanner that has nothing left to yield.
			out <- ollama.StreamEvent{ToolCalls: tools.result(), Done: true, DoneReason: stopReason}
			c.logStop(stopReason, model, maxTokens)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("scan: %w", err)}
		return
	}

	// The stream ended without message_stop (upstream closed early). Everything
	// accumulated so far is still worth delivering.
	out <- ollama.StreamEvent{ToolCalls: tools.result(), Done: true, DoneReason: stopReason}
	c.logStop(stopReason, model, maxTokens)
}

// logStop reports a turn that ended because it ran out of room rather than
// because the model was finished, which otherwise looks like a clean stop with a
// mysteriously truncated answer.
func (c *Client) logStop(stopReason, model string, maxTokens int) {
	if stopReason == "max_tokens" {
		log.Printf("[anthropic] generation hit the %d-token cap (truncated turn) — model %q", maxTokens, model)
	}
}

// ---- models / health ----------------------------------------------------

// knownModels is the last-resort list for pickers when Anthropic will not
// enumerate models for this credential. It is not authoritative and goes stale:
// /v1/models is the real source, and the configured model is always offered
// first.
var knownModels = []string{
	"claude-opus-4-5",
	"claude-sonnet-4-5",
	"claude-haiku-4-5",
}

// ContextBudgetChars returns 0: Claude owns its own (very large) context window
// and truncates server-side, so the agent's live compaction need not enforce a
// token-derived cap on this backend's behalf.
func (c *Client) ContextBudgetChars() int { return 0 }

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/models?limit=100", nil)
	if err != nil {
		return nil, err
	}
	if err := c.auth(req); err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.fallbackModels(), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// The key is dead or wrong. Serving the fallback list here would put Claude
		// models in the picker that cannot answer — better to report it and let the
		// caller leave Claude out. Matches Ping's reading of 401 vs 403.
		b, _ := readLimited(resp.Body)
		return nil, fmt.Errorf("anthropic rejected the API key (401): %s", strings.TrimSpace(string(b)))
	}
	if resp.StatusCode != http.StatusOK {
		// A key can authenticate without being allowed to enumerate models (403).
		// That is not a broken backend, so serve the fallback rather than leaving
		// the picker empty.
		log.Printf("[anthropic] /v1/models returned %d — using the fallback model list", resp.StatusCode)
		return c.fallbackModels(), nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return c.fallbackModels(), nil
	}
	if len(result.Data) == 0 {
		return c.fallbackModels(), nil
	}
	names := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		names = append(names, m.ID)
	}
	return names, nil
}

func (c *Client) fallbackModels() []string {
	out := make([]string, 0, len(knownModels)+1)
	seen := map[string]bool{}
	if c.fallbackModel != "" {
		out = append(out, c.fallbackModel)
		seen[c.fallbackModel] = true
	}
	for _, m := range knownModels {
		if !seen[m] {
			out = append(out, m)
		}
	}
	return out
}

// Ping reports whether the backend can authenticate. It deliberately does not
// send a Messages request: on a subscription every call draws down the plan's
// usage window, and a health check is not worth a slice of it.
func (c *Client) Ping(ctx context.Context) error {
	if err := ValidateKey(c.apiKey); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/models?limit=1", nil)
	if err != nil {
		return err
	}
	if err := c.auth(req); err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("anthropic rejected the API key (401) — check ANTHROPIC_API_KEY")
	case resp.StatusCode == http.StatusForbidden:
		// The token authenticates but isn't scoped to enumerate models, which
		// says nothing about whether inference works. Treat it as healthy.
		return nil
	case resp.StatusCode >= 400:
		b, _ := readLimited(resp.Body)
		return fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// readLimited reads an error body without letting a misbehaving upstream stream
// an unbounded one into memory.
func readLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 4096))
}
