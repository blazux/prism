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
	"net/http"
	"strings"
	"time"

	"prism/internal/ollama"
)

type Client struct {
	baseURL    string // includes the /v1 suffix, e.g. http://host:30000/v1
	apiKey     string // optional; local SGLang/vLLM usually need none
	httpClient *http.Client
}

// NewClient builds an OpenAI-compatible client. baseURL should point at the /v1
// root (a trailing slash is tolerated). apiKey may be empty.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 0}, // no timeout for streaming
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
				id := fmt.Sprintf("call_%d", seq)
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
			id := ""
			if len(pending) > 0 {
				id = pending[0]
				pending = pending[1:]
			}
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

func (c *Client) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	payload := chatRequest{
		Model:       req.Model,
		Messages:    buildMessages(req.Messages),
		Stream:      true,
		Temperature: req.Options.Temperature,
	}
	for _, t := range req.Tools {
		payload.Tools = append(payload.Tools, chatTool{Type: "function", Function: t.Function})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("marshal: %w", err)}
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("request: %w", err)}
		return
	}
	c.auth(httpReq)

	resp, err := c.httpClient.Do(httpReq)
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
	}

	if err := scanner.Err(); err != nil {
		out <- ollama.StreamEvent{Err: fmt.Errorf("scan: %w", err)}
		return
	}

	out <- ollama.StreamEvent{ToolCalls: tools.result(), Done: true}
}

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
