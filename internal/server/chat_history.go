package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

func (s *Server) recordChatError(ctx context.Context, sessionID, message string) {
	if ms := s.store(); ms != nil && message != "" {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		message = strings.ToValidUTF8(strings.ReplaceAll(message, "\x00", ""), "�")
		if len(message) > 8000 {
			message = strings.ToValidUTF8(message[:8000], "�") + "…"
		}
		if _, err := ms.AppendMessage(ctx, sessionID, "error", message, nil); err != nil {
			log.Printf("[chat] could not persist error diagnostic: session=%q", sessionID)
		}
	}
}

func (s *Server) sendHistory(ctx context.Context, client *Client, sessionID string) {
	ms := s.store()
	if ms != nil {
		if entries, err := ms.LoadHistory(ctx, sessionID); err == nil && len(entries) > 0 {
			type toolCallDef struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			}
			type histMsg struct {
				Role      string          `json:"role"`
				Content   string          `json:"content"`
				CreatedAt string          `json:"createdAt,omitempty"`
				ToolName  string          `json:"toolName,omitempty"`
				ToolInput json.RawMessage `json:"toolInput,omitempty"`
			}
			var msgs []histMsg
			var pendingCalls []toolCallDef
			var callIdx int
			for _, e := range entries {
				switch e.Role {
				case "error":
					msgs = append(msgs, histMsg{Role: "error", Content: e.Content, CreatedAt: e.CreatedAt.Local().Format(time.RFC3339)})
				case "user":
					msgs = append(msgs, histMsg{
						Role:      "user",
						Content:   e.Content,
						CreatedAt: e.CreatedAt.Local().Format(time.RFC3339),
					})
					pendingCalls = nil
					callIdx = 0
				case "assistant":
					if strings.TrimSpace(e.Content) != "" {
						msgs = append(msgs, histMsg{
							Role:      "assistant",
							Content:   e.Content,
							CreatedAt: e.CreatedAt.Local().Format(time.RFC3339),
						})
					}
					pendingCalls = nil
					callIdx = 0
					if len(e.ToolCalls) > 0 && string(e.ToolCalls) != "null" {
						_ = json.Unmarshal(e.ToolCalls, &pendingCalls)
					}
				case "tool":
					m := histMsg{Role: "tool", Content: e.Content}
					if callIdx < len(pendingCalls) {
						m.ToolName = pendingCalls[callIdx].Function.Name
						m.ToolInput = pendingCalls[callIdx].Function.Arguments
						callIdx++
					}
					msgs = append(msgs, m)
				}
			}
			if len(msgs) > 0 {
				client.sendJSON(map[string]interface{}{
					"type":     "chat_history",
					"messages": msgs,
				})
			}
		}
	}

}
