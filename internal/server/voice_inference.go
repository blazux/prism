package server

// The gateway supplies its own telephone prompt/history/tools. This relay only
// resolves an installation AI source; it does not run a Prism agent or execute
// any requested tool. Provider credentials never leave Prism.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"prism/internal/ollama"
)

type voiceWireMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

type voiceWireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

func (s *Server) handleVoiceInference(w http.ResponseWriter, r *http.Request) {
	if !canManageTelephony(s, r) {
		writeErr(w, 403, "phone gateway only")
		return
	}
	ai, err := s.aiConfigFor(r.Context(), 0)
	if err != nil {
		writeErr(w, 503, "AI configuration unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/voice/inference/v1/")
	c := s.voxConnection()
	if path == "models" && r.Method == http.MethodGet {
		model := c.Model
		if model == "" {
			model = "default"
		}
		selected := model
		if selected == "default" {
			selected = ai.cfg.Model
		}
		backend := ai.chatBackendFor(selected)
		probe, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if routed, ok := backend.(*sourcesBackend); ok {
			source, _, routeErr := routed.resolve(probe, selected)
			if routeErr != nil {
				writeErr(w, 503, "the phone model source is unavailable")
				return
			}
			backend = source.profile().backendConfig().newChatBackend()
		}
		if _, probeErr := backend.ListModels(probe); probeErr != nil {
			writeErr(w, 503, "the phone model source is unavailable")
			return
		}
		writeJSON(w, map[string]any{"object": "list", "data": []any{map[string]any{"id": model, "object": "model", "owned_by": "prism"}}})
		return
	}
	if path != "chat/completions" || r.Method != http.MethodPost {
		writeErr(w, 405, "unsupported inference operation")
		return
	}
	var in struct {
		Model               string             `json:"model"`
		Messages            []voiceWireMessage `json:"messages"`
		Tools               []voiceWireTool    `json:"tools"`
		Stream              bool               `json:"stream"`
		Temperature         float64            `json:"temperature"`
		MaxTokens           int                `json:"max_tokens"`
		MaxCompletionTokens int                `json:"max_completion_tokens"`
		PresencePenalty     float64            `json:"presence_penalty"`
		TopP                *float64           `json:"top_p"`
		TopK                *int               `json:"top_k"`
		ReasoningEffort     string             `json:"reasoning_effort"`
		ChatTemplateKwargs  map[string]any     `json:"chat_template_kwargs"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&in) != nil || len(in.Messages) == 0 {
		writeErr(w, 400, "invalid inference request")
		return
	}
	if s.voxGatewayAuthed(r) {
		approved := c.Model != "" && in.Model == c.Model
		for _, previous := range c.PreviousModels {
			if in.Model == previous {
				approved = true
				break
			}
		}
		if !approved {
			writeErr(w, 403, "select the phone model from Telephony settings")
			return
		}
	}
	model := in.Model
	if model == "" || model == "default" {
		model = ai.cfg.Model
	}
	if allowed, unrestricted := s.platformAllowedModels(r.Context()); !unrestricted && !modelInSet(allowed, model) {
		writeErr(w, 403, "the phone model is disabled by policy")
		return
	}
	if in.MaxTokens <= 0 {
		in.MaxTokens = in.MaxCompletionTokens
	}
	if in.MaxTokens < 0 || in.MaxTokens > 131072 || in.Temperature < 0 || in.Temperature > 2 || (in.TopP != nil && (*in.TopP < 0 || *in.TopP > 1)) || (in.TopK != nil && *in.TopK < 0) {
		writeErr(w, 400, "invalid generation settings")
		return
	}
	effort := strings.TrimSpace(in.ReasoningEffort)
	thinking, explicitThinking := in.ChatTemplateKwargs["enable_thinking"].(bool)
	noThinking := effort == "none" || (explicitThinking && !thinking)
	if effort == "none" {
		effort = ""
	}
	req := ollama.ChatRequest{Model: model, Stream: true, NoThinking: noThinking, ReasoningEffort: effort, Options: ollama.Options{Temperature: in.Temperature, NumPredict: in.MaxTokens, PresencePenalty: in.PresencePenalty, TopP: in.TopP, TopK: in.TopK}}
	for _, tool := range in.Tools {
		if tool.Type != "function" || tool.Function.Name == "" {
			writeErr(w, 400, "unsupported tool schema")
			return
		}
		schema := tool.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(schema, &object) != nil || object == nil {
			writeErr(w, 400, "tool parameters must be a schema object")
			return
		}
		req.Tools = append(req.Tools, ollama.Tool{Type: "function", Function: ollama.ToolFunction{Name: tool.Function.Name, Description: tool.Function.Description, Parameters: ollama.ToolParameters{RawSchema: schema}}})
	}
	if explicitThinking {
		req.EnableThinking = &thinking
	}
	for _, m := range in.Messages {
		if m.Role != "system" && m.Role != "assistant" && m.Role != "user" && m.Role != "tool" {
			writeErr(w, 400, "unsupported message role")
			return
		}
		message := ollama.Message{Role: m.Role, Content: m.Content}
		for _, call := range m.ToolCalls {
			if !json.Valid([]byte(call.Function.Arguments)) {
				writeErr(w, 400, "invalid tool arguments")
				return
			}
			message.ToolCalls = append(message.ToolCalls, ollama.ToolCall{Function: ollama.ToolCallFunction{Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)}})
		}
		req.Messages = append(req.Messages, message)
	}
	backend := ai.chatBackendFor(model)
	events := make(chan ollama.StreamEvent, 16)
	go func() { defer close(events); backend.Chat(r.Context(), req, events) }()
	id := fmt.Sprintf("vox-%x", time.Now().UnixNano())
	var text strings.Builder
	var calls []map[string]any
	finish := ""
	started := false
	w.Header().Set("Cache-Control", "no-store")
	emit := func(delta any, reason any) {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			started = true
		}
		chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "model": in.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}}
		raw, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", raw)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	failed := false
	for event := range events {
		if event.Err != nil {
			failed = true
			continue
		}
		if failed {
			continue
		}
		if event.Content != "" {
			text.WriteString(event.Content)
			if in.Stream {
				emit(map[string]any{"content": event.Content}, nil)
			}
		}
		for _, call := range event.ToolCalls {
			args := string(call.Function.Arguments)
			if call.Function.ArgumentError != "" || !json.Valid(call.Function.Arguments) {
				failed = true
				break
			}
			n := len(calls)
			wire := map[string]any{"id": fmt.Sprintf("%09d", n+1), "type": "function", "function": map[string]any{"name": call.Function.Name, "arguments": args}}
			calls = append(calls, wire)
			if in.Stream {
				delta := map[string]any{}
				for k, v := range wire {
					delta[k] = v
				}
				delta["index"] = n
				emit(map[string]any{"tool_calls": []any{delta}}, nil)
			}
		}
		if event.Done {
			finish = event.DoneReason
			if finish == "" {
				finish = "stop"
			}
			if finish == "end_turn" || finish == "stop_sequence" {
				finish = "stop"
			}
			if finish == "max_tokens" {
				finish = "length"
			}
			if len(calls) > 0 && (finish == "stop" || finish == "tool_use") {
				finish = "tool_calls"
			}
		}
	}
	if failed {
		if !started {
			writeErr(w, 502, "the phone model returned an invalid or incomplete response")
		} else {
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"the phone model returned an invalid or incomplete response\",\"type\":\"upstream_error\"}}\n\n")
		}
		return
	}
	if finish == "" || finish == "interrupted" {
		if !started {
			writeErr(w, 502, "the phone model stream ended before completion")
		} else {
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"stream interrupted\"}}\n\n")
		}
		return
	}
	if in.Stream {
		emit(map[string]any{}, finish)
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	message := map[string]any{"role": "assistant", "content": text.String()}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	writeJSON(w, map[string]any{"id": id, "object": "chat.completion", "model": in.Model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
}
