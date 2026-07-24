package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
}

// DefaultNumPredict caps the tokens generated for one turn when the caller asks for
// no specific ceiling. Ollama's own default is -1: generate until the context is
// full. A model that loops instead of emitting a stop token then streams for as long
// as that takes, with the caller blocked and nothing to log — which is exactly how a
// chat ends up never answering. Generous enough for a whole widget or a long note;
// hitting it truncates the turn instead of hanging it.
const DefaultNumPredict = 16384

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
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
	Options  Options   `json:"options,omitempty"`
	// NoThinking asks the model to skip extended reasoning for this turn. Set on
	// the voice channel, where a caller waits in silence while the model thinks.
	// Wire-neutral (each backend translates it); not sent to Ollama as-is.
	NoThinking bool `json:"-"`
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
	Model   string  `json:"model"`
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

type StreamEvent struct {
	Content   string
	Thinking  string
	ToolCalls []ToolCall
	Done      bool
	Err       error
}

func (c *Client) Chat(ctx context.Context, req ChatRequest, out chan<- StreamEvent) {
	req.Stream = true
	if req.Options.NumPredict == 0 {
		req.Options.NumPredict = DefaultNumPredict
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
		out <- StreamEvent{Err: fmt.Errorf("ollama returned %d", resp.StatusCode)}
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
			Content:   chunk.Message.Content,
			Thinking:  chunk.Message.Thinking,
			ToolCalls: chunk.Message.ToolCalls,
			Done:      chunk.Done,
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
