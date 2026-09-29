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

type apiRequest struct {
	Model        string        `json:"model"`
	System       []textBlock   `json:"system,omitempty"`
	Messages     []apiMessage  `json:"messages"`
	Tools        []apiTool     `json:"tools,omitempty"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
	MaxTokens    int           `json:"max_tokens"`
	Temperature  *float64      `json:"temperature,omitempty"`
	Stream       bool          `json:"stream"`
}

type textBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type apiMessage struct {
	Role    string     `json:"role"`
	Content []apiBlock `json:"content"`
}

// apiBlock is the union of every content block shape we emit: text, image,
// tool_use (replayed assistant calls) and tool_result.
type apiBlock struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`

	Text string `json:"text,omitempty"`

	Source *imageSource `json:"source,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

// Tool-search blocks must be replayed exactly, including provider-only fields.
func (b apiBlock) MarshalJSON() ([]byte, error) {
	if len(b.Raw) > 0 {
		return b.Raw, nil
	}
	type plain apiBlock
	return json.Marshal(plain(b))
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type apiTool struct {
	Type         string                `json:"type,omitempty"`
	Name         string                `json:"name"`
	Description  string                `json:"description"`
	InputSchema  ollama.ToolParameters `json:"input_schema"`
	DeferLoading bool                  `json:"defer_loading,omitempty"`
	CacheControl *cacheControl         `json:"cache_control,omitempty"`
}

// Server tools have no client-side input schema or description.
func (t apiTool) MarshalJSON() ([]byte, error) {
	if t.Type != "" {
		return json.Marshal(struct {
			Type         string        `json:"type"`
			Name         string        `json:"name"`
			CacheControl *cacheControl `json:"cache_control,omitempty"`
		}{t.Type, t.Name, t.CacheControl})
	}
	type plain apiTool
	return json.Marshal(plain(t))
}

// buildTools converts the pivot tool list.
func buildTools(in []ollama.Tool) []apiTool {
	return buildToolsWithCachePrefix(in, 0)
}

func buildToolsWithCachePrefix(in []ollama.Tool, nativeCount int) []apiTool {
	if len(in) == 0 {
		return nil
	}
	out := make([]apiTool, 0, len(in))
	for _, t := range in {
		schema := t.Function.Parameters
		if schema.Type == "" {
			schema.Type = "object" // Anthropic rejects a schema with no type
		}
		out = append(out, apiTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	// Native tools precede custom/MCP tools. Preserve their cache even when a
	// custom tool is registered during this turn. The final breakpoint still
	// caches the full tool set once it stabilizes.
	if nativeCount > 0 && nativeCount < len(out) {
		out[nativeCount-1].CacheControl = &cacheControl{Type: "ephemeral"}
	}
	// Tool definitions precede the system prompt and conversation in Anthropic's
	// cache key. They are large and generally unchanged across an agent loop.
	out[len(out)-1].CacheControl = &cacheControl{Type: "ephemeral"}
	return out
}

// buildToolSearchTools keeps basic file/code tools immediately available and
// lets Claude discover the rest of the catalog when it needs them.
func buildToolSearchTools(in []ollama.Tool) []apiTool {
	base := buildTools(in)
	visible := []apiTool{{Type: "tool_search_tool_bm25_20251119", Name: "tool_search_tool_bm25"}}
	var deferred []apiTool
	for _, t := range base {
		t.CacheControl = nil // Anthropic rejects cache_control on deferred tools.
		switch t.Name {
		case "exec_command", "read_file", "write_file", "widget", "cron":
			visible = append(visible, t)
		default:
			t.DeferLoading = true
			deferred = append(deferred, t)
		}
	}
	visible[len(visible)-1].CacheControl = &cacheControl{Type: "ephemeral"}
	return append(visible, deferred...)
}

// buildSystem extracts the system turns from the pivot history into the
// top-level system field, where this API expects them.
func buildSystem(in []ollama.Message) []textBlock {
	return buildSystemWithCachePrefix(in, 0)
}

func buildSystemWithCachePrefix(in []ollama.Message, prefixBytes int) []textBlock {
	var blocks []textBlock
	for _, m := range in {
		if m.Role != "system" {
			continue
		}
		if prefixBytes > 0 && prefixBytes <= len(m.Content) {
			prefix := m.Content[:prefixBytes]
			if strings.TrimSpace(prefix) != "" {
				blocks = append(blocks, textBlock{Type: "text", Text: prefix, CacheControl: &cacheControl{Type: "ephemeral"}})
			}
			if suffix := m.Content[prefixBytes:]; strings.TrimSpace(suffix) != "" {
				blocks = append(blocks, textBlock{Type: "text", Text: suffix})
			}
			prefixBytes = 0
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
func buildMessages(in []ollama.Message) []apiMessage {
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
			if len(m.ProviderBlocks) > 0 {
				var replay []apiBlock
				for _, raw := range m.ProviderBlocks {
					var header struct{ Type, ID string }
					if json.Unmarshal(raw, &header) != nil {
						continue
					}
					replay = append(replay, apiBlock{Type: header.Type, ID: header.ID, Raw: raw})
					if header.Type == "tool_use" {
						pending = append(pending, header.ID)
					}
				}
				if len(replay) > 0 {
					appendBlocks("assistant", replay...)
					continue
				}
			}
			// Without live provider blocks (e.g. after restart), replay the
			// ordinary tool calls; Claude can search again if needed.
			var blocks []apiBlock
			if text := strings.TrimSpace(m.Content); text != "" {
				blocks = append(blocks, apiBlock{Type: "text", Text: text})
			}
			for _, tc := range m.ToolCalls {
				seq++
				id := fmt.Sprintf("toolu_%08d", seq)
				pending = append(pending, id)
				input := json.RawMessage(tc.Function.Arguments)
				if len(input) == 0 || !json.Valid(input) {
					// A truncated turn can leave unparseable arguments in the
					// history. Replaying them 400s the whole request, and every
					// turn after it, so fall back to an empty object.
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, apiBlock{Type: "tool_use", ID: id, Name: tc.Function.Name, Input: input})
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
