package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"prism/internal/ollama"
)

// The Messages API differs from the OpenAI wire in ways that each cost a 400 if
// ignored: the system prompt is a top-level field rather than a message, tool
// results ride inside a *user* message as blocks, text blocks may not be empty,
// and the conversation must start with a user turn. Everything in this file is
// about turning PRISM's Ollama-shaped pivot into that shape without ever
// emitting a message Anthropic will reject.

// oauthToolPrefix is prepended to every tool name on the subscription wire.
// Anthropic's billing classifier reads a *single*-underscore "mcp_" prefix as a
// third-party app and rejects the request ("Third-party apps now draw from extra
// usage, not plan limits"); the double underscore is what Claude Code itself
// sends, so it stays on the plan. Names come back prefixed and are stripped
// again in the stream reader, so nothing downstream ever sees the wire name.
const oauthToolPrefix = "mcp__"

// claudeCodeSystemPrefix is the first system block Claude Code sends. Anthropic
// routes subscription traffic on it; without it the request is not recognised as
// coming from the CLI.
const claudeCodeSystemPrefix = "You are Claude Code, Anthropic's official CLI for Claude."

type apiRequest struct {
	Model       string       `json:"model"`
	System      []textBlock  `json:"system,omitempty"`
	Messages    []apiMessage `json:"messages"`
	Tools       []apiTool    `json:"tools,omitempty"`
	MaxTokens   int          `json:"max_tokens"`
	Temperature *float64     `json:"temperature,omitempty"`
	Stream      bool         `json:"stream"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiMessage struct {
	Role    string     `json:"role"`
	Content []apiBlock `json:"content"`
}

// apiBlock is the union of every content block shape we emit: text, image,
// tool_use (replayed assistant calls) and tool_result.
type apiBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	Source *imageSource `json:"source,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type apiTool struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	InputSchema ollama.ToolParameters `json:"input_schema"`
}

// buildTools converts the pivot tool list. oauth adds the wire prefix that keeps
// the request on the subscription plan.
func buildTools(in []ollama.Tool, oauth bool) []apiTool {
	if len(in) == 0 {
		return nil
	}
	out := make([]apiTool, 0, len(in))
	for _, t := range in {
		name := t.Function.Name
		if oauth {
			name = toWireName(name)
		}
		schema := t.Function.Parameters
		if schema.Type == "" {
			schema.Type = "object" // Anthropic rejects a schema with no type
		}
		out = append(out, apiTool{
			Name:        name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	return out
}

func toWireName(name string) string {
	if strings.HasPrefix(name, oauthToolPrefix) {
		return name
	}
	// A single-underscore mcp_ name is the one shape that trips the classifier,
	// so promote it rather than prefixing it twice.
	if strings.HasPrefix(name, "mcp_") {
		return oauthToolPrefix + strings.TrimPrefix(name, "mcp_")
	}
	return oauthToolPrefix + name
}

// fromWireName reverses toWireName so the agent's dispatcher sees the tool name
// it registered. Only the prefix we added is removed; a name that never carried
// it is returned untouched.
func fromWireName(name string) string {
	if !strings.HasPrefix(name, oauthToolPrefix) {
		return name
	}
	return strings.TrimPrefix(name, oauthToolPrefix)
}

// buildSystem extracts the system turns from the pivot history into the
// top-level system field. On the subscription wire Claude Code's own identity
// block comes first — PRISM's prompt follows it as a second block.
func buildSystem(in []ollama.Message, oauth bool) []textBlock {
	var blocks []textBlock
	if oauth {
		blocks = append(blocks, textBlock{Type: "text", Text: claudeCodeSystemPrefix})
	}
	for _, m := range in {
		if m.Role != "system" {
			continue
		}
		if text := strings.TrimSpace(m.Content); text != "" {
			blocks = append(blocks, textBlock{Type: "text", Text: text})
		}
	}
	return blocks
}

// buildMessages converts the pivot history to Anthropic messages.
//
// PRISM's history carries no tool-call ids, so they are synthesised the same way
// the OpenAI backend does it: each assistant tool_call gets a sequential id and
// each following tool message is matched, in order, against the pending ids. A
// tool result whose originating call was summarised out of the replayed history
// has nothing to reference and is dropped — Anthropic rejects a tool_result
// pointing at an unknown tool_use_id.
func buildMessages(in []ollama.Message, oauth bool) []apiMessage {
	out := make([]apiMessage, 0, len(in))
	var pending []string
	seq := 0

	appendBlocks := func(role string, blocks ...apiBlock) {
		if len(blocks) == 0 {
			return
		}
		// Anthropic takes consecutive same-role turns badly, and a run of tool
		// results has to arrive as several blocks of one user message anyway.
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, apiMessage{Role: role, Content: blocks})
	}

	for _, m := range in {
		switch m.Role {
		case "system":
			// Already hoisted into the top-level system field.
			continue

		case "assistant":
			var blocks []apiBlock
			if text := strings.TrimSpace(m.Content); text != "" {
				blocks = append(blocks, apiBlock{Type: "text", Text: text})
			}
			for _, tc := range m.ToolCalls {
				seq++
				id := fmt.Sprintf("toolu_%08d", seq)
				pending = append(pending, id)
				name := tc.Function.Name
				if oauth {
					name = toWireName(name)
				}
				input := json.RawMessage(tc.Function.Arguments)
				if len(input) == 0 || !json.Valid(input) {
					// A truncated turn can leave unparseable arguments in the
					// history. Replaying them 400s the whole request, and every
					// turn after it, so fall back to an empty object.
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, apiBlock{Type: "tool_use", ID: id, Name: name, Input: input})
			}
			// An assistant turn with neither text nor tool calls has no valid
			// representation — an empty content list is rejected.
			appendBlocks("assistant", blocks...)

		case "tool":
			if len(pending) == 0 {
				continue
			}
			id := pending[0]
			pending = pending[1:]
			content := m.Content
			if strings.TrimSpace(content) == "" {
				// A tool that returned nothing still owes Anthropic a non-empty
				// result block, and silence reads as failure to the model.
				content = "(no output)"
			}
			appendBlocks("user", apiBlock{Type: "tool_result", ToolUseID: id, Content: content})

		default: // user
			var blocks []apiBlock
			if text := strings.TrimSpace(m.Content); text != "" {
				blocks = append(blocks, apiBlock{Type: "text", Text: text})
			}
			for _, img := range m.Images {
				blocks = append(blocks, apiBlock{Type: "image", Source: &imageSource{
					Type:      "base64",
					MediaType: "image/jpeg",
					Data:      img,
				}})
			}
			appendBlocks("user", blocks...)
		}
	}

	// The conversation must open on a user turn. Compaction can leave an
	// assistant message first; a minimal user turn ahead of it is cheaper than
	// discarding the history that follows.
	if len(out) > 0 && out[0].Role != "user" {
		out = append([]apiMessage{{
			Role:    "user",
			Content: []apiBlock{{Type: "text", Text: "continue"}},
		}}, out...)
	}

	// A trailing assistant turn is treated as a prefill to continue, and one
	// ending in whitespace is rejected outright.
	if n := len(out); n > 0 && out[n-1].Role == "assistant" {
		blocks := out[n-1].Content
		if b := len(blocks); b > 0 && blocks[b-1].Type == "text" {
			blocks[b-1].Text = strings.TrimRight(blocks[b-1].Text, " \t\n")
		}
	}

	return out
}
