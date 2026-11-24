package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"prism/internal/ollama"
)

const (
	maxProviderReplayBytes  = 128 * 1024
	maxProviderReplayBlocks = 256
)

// Provider replay metadata is a typed, bounded checkpoint, not a copy of the
// provider's arbitrary response. Client arguments remain in canonical,
// redacted tool_calls; this envelope stores only their original IDs and order.
type providerReplayEnvelope struct {
	Provider string            `json:"provider"`
	Version  int               `json:"version"`
	Blocks   []json.RawMessage `json:"blocks"`
}

type replayTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type replayServerToolBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type replayToolReference struct {
	Type     string `json:"type"`
	ToolName string `json:"tool_name"`
}

type replaySearchContent struct {
	Type           string                `json:"type"`
	ToolReferences []replayToolReference `json:"tool_references"`
}

type replaySearchResult struct {
	Type      string              `json:"type"`
	ToolUseID string              `json:"tool_use_id"`
	Content   replaySearchContent `json:"content"`
}

type replayClientTool struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	CallIndex *int   `json:"call_index"`
}

func replayJSON(raw json.RawMessage, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}

func replayIdentifier(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, " \t\r\n\x00")
}

func replayBlockType(raw json.RawMessage) string {
	var header struct{ Type string }
	if json.Unmarshal(raw, &header) != nil {
		return ""
	}
	return header.Type
}

// normalizeReplayBlock accepts only the documented tool-search shapes. Unknown
// fields, thinking/signatures and provider errors fall back to ordinary history.
func normalizeReplayBlock(raw json.RawMessage) json.RawMessage {
	var value any
	switch replayBlockType(raw) {
	case "text":
		var block replayTextBlock
		if !replayJSON(raw, &block) || block.Text == "" || strings.ContainsRune(block.Text, 0) {
			return nil
		}
		lower := strings.ToLower(block.Text)
		for _, tag := range []string{"<think>", "<thought>", "<thinking>"} {
			if strings.Contains(lower, tag) {
				return nil // never duplicate hidden reasoning in the checkpoint
			}
		}
		value = block
	case "server_tool_use":
		var block replayServerToolBlock
		if !replayJSON(raw, &block) || !replayIdentifier(block.ID) || !strings.HasPrefix(block.ID, "srvtoolu_") {
			return nil
		}
		input := redactToolArgs(block.Input)
		var query struct {
			Query *string `json:"query"`
			Limit *int    `json:"limit,omitempty"`
		}
		var pattern struct {
			Pattern *string `json:"pattern"`
			Limit   *int    `json:"limit,omitempty"`
		}
		switch block.Name {
		case "tool_search_tool_bm25":
			if !replayJSON(input, &query) || query.Query == nil || utf8.RuneCountInString(*query.Query) > 500 || strings.ContainsRune(*query.Query, 0) || query.Limit != nil && (*query.Limit < 1 || *query.Limit > 10000) {
				return nil
			}
			block.Input, _ = json.Marshal(query)
		case "tool_search_tool_regex":
			if !replayJSON(input, &pattern) || pattern.Pattern == nil || utf8.RuneCountInString(*pattern.Pattern) > 200 || strings.ContainsRune(*pattern.Pattern, 0) || pattern.Limit != nil && (*pattern.Limit < 1 || *pattern.Limit > 10000) {
				return nil
			}
			block.Input, _ = json.Marshal(pattern)
		default:
			return nil
		}
		value = block
	case "tool_search_tool_result":
		var block replaySearchResult
		if !replayJSON(raw, &block) || !replayIdentifier(block.ToolUseID) || !strings.HasPrefix(block.ToolUseID, "srvtoolu_") || block.Content.Type != "tool_search_tool_search_result" || block.Content.ToolReferences == nil {
			return nil
		}
		for _, ref := range block.Content.ToolReferences {
			if ref.Type != "tool_reference" || !replayIdentifier(ref.ToolName) {
				return nil
			}
		}
		value = block
	default:
		return nil
	}
	result, _ := json.Marshal(value)
	return result
}

// providerReplayMetadata prepares optional DB state without persisting raw
// client inputs. Unsupported state keeps the existing model-neutral replay.
func providerReplayMetadata(message ollama.Message) json.RawMessage {
	if message.Role != "assistant" || len(message.ProviderBlocks) == 0 || len(message.ProviderBlocks) > maxProviderReplayBlocks {
		return nil
	}
	envelope := providerReplayEnvelope{Provider: "anthropic", Version: 1}
	clientIndex, useful := 0, false
	seen := make(map[string]bool)
	for _, raw := range message.ProviderBlocks {
		var stored json.RawMessage
		if replayBlockType(raw) == "tool_use" {
			var block replayServerToolBlock // same wire fields, different dispatch
			if !replayJSON(raw, &block) || !replayIdentifier(block.ID) || !replayIdentifier(block.Name) || seen[block.ID] || clientIndex >= len(message.ToolCalls) || message.ToolCalls[clientIndex].Function.Name != block.Name {
				return nil
			}
			if !bytes.Equal(redactToolArgs(block.Input), redactToolArgs(message.ToolCalls[clientIndex].Function.Arguments)) {
				return nil
			}
			seen[block.ID] = true
			index := clientIndex
			stored, _ = json.Marshal(replayClientTool{Type: "tool_use", ID: block.ID, Name: block.Name, CallIndex: &index})
			clientIndex++
			useful = true
		} else {
			stored = normalizeReplayBlock(raw)
			if stored == nil {
				return nil
			}
			var header struct{ Type, ID string }
			_ = json.Unmarshal(stored, &header)
			if header.ID != "" {
				if seen[header.ID] {
					return nil
				}
				seen[header.ID] = true
			}
			useful = useful || header.Type != "text"
		}
		envelope.Blocks = append(envelope.Blocks, stored)
	}
	if !useful || clientIndex != len(message.ToolCalls) {
		return nil
	}
	metadata, _ := json.Marshal(envelope)
	if len(metadata) > maxProviderReplayBytes || restoreProviderReplay(metadata, message.ToolCalls) == nil {
		return nil
	}
	return metadata
}

// restoreProviderReplay restores only replay data, never executes tools. The
// inputs are reconstructed from canonical calls and redacted again defensively.
// A server-only pause is valid; a later result may refer to a previous message.
func restoreProviderReplay(metadata json.RawMessage, calls []ollama.ToolCall) []json.RawMessage {
	if len(metadata) == 0 || len(metadata) > maxProviderReplayBytes {
		return nil
	}
	var envelope providerReplayEnvelope
	if !replayJSON(metadata, &envelope) || envelope.Provider != "anthropic" || envelope.Version != 1 || len(envelope.Blocks) == 0 || len(envelope.Blocks) > maxProviderReplayBlocks {
		return nil
	}
	canonical := redactedToolCalls(calls)
	var blocks []json.RawMessage
	clientIndex := 0
	seen := make(map[string]bool)
	for _, stored := range envelope.Blocks {
		var raw json.RawMessage
		if replayBlockType(stored) == "tool_use" {
			var block replayClientTool
			if !replayJSON(stored, &block) || !replayIdentifier(block.ID) || !replayIdentifier(block.Name) || seen[block.ID] || block.CallIndex == nil || *block.CallIndex != clientIndex || clientIndex >= len(canonical) || canonical[clientIndex].Function.Name != block.Name {
				return nil
			}
			input := canonical[clientIndex].Function.Arguments
			var object map[string]json.RawMessage
			if json.Unmarshal(input, &object) != nil || object == nil {
				return nil
			}
			raw, _ = json.Marshal(replayServerToolBlock{Type: "tool_use", ID: block.ID, Name: block.Name, Input: input})
			clientIndex++
		} else {
			raw = normalizeReplayBlock(stored)
			if raw == nil {
				return nil
			}
		}
		var header struct{ ID string }
		_ = json.Unmarshal(raw, &header)
		if header.ID != "" {
			if seen[header.ID] {
				return nil
			}
			seen[header.ID] = true
		}
		blocks = append(blocks, raw)
	}
	if clientIndex != len(canonical) {
		return nil
	}
	return blocks
}
