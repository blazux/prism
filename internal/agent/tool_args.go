package agent

import (
	"encoding/json"
	"strings"

	"prism/internal/ollama"
)

// redactToolArgs removes credential values before tool calls leave the
// execution path (history, logs and browser events). Tool execution itself
// always receives the original arguments. Secret names such as password_secret
// remain visible because they are identifiers, not credential values.
func redactToolArgs(raw json.RawMessage) json.RawMessage {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	redactJSONValue(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return redacted
}

func redactJSONValue(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if sensitiveArgumentKey(key) {
				v[key] = "[REDACTED]"
				continue
			}
			redactJSONValue(child)
		}
	case []any:
		for _, child := range v {
			redactJSONValue(child)
		}
	}
}

func sensitiveArgumentKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "password", "passwd", "api_key", "apikey", "access_token", "refresh_token", "client_secret", "secret_value":
		return true
	}
	return strings.HasSuffix(key, "_password") || strings.HasSuffix(key, "_token")
}

func redactedToolCalls(calls []ollama.ToolCall) []ollama.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ollama.ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		out[i].Function.Arguments = redactToolArgs(out[i].Function.Arguments)
	}
	return out
}
