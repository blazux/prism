package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleExternalNotify allows cron scripts (running in Docker) to push notifications
//
//	via: curl -s -X POST http://prism-server:8080/api/notify -H "Content-Type: application/json" \
//	     -d '{"session":"default","title":"Backup OK","level":"success"}'
func (s *Server) handleExternalNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var body struct {
		Session string `json:"session"`
		Title   string `json:"title"`
		Message string `json:"message"`
		Level   string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		http.Error(w, "title required", 400)
		return
	}
	if body.Session == "" {
		body.Session = "default"
	}
	if body.Level == "" {
		body.Level = "info"
	}

	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		http.Error(w, "memory store not available", 503)
		return
	}

	id, err := ms.AddNotification(r.Context(), body.Session, body.Title, body.Message, body.Level)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"id": id})
}

// pushJSONToSession sends an arbitrary JSON payload to all live WS clients for a session.
func (s *Server) pushJSONToSession(sessionID string, payload map[string]interface{}) {
	data, _ := json.Marshal(payload)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if c.sessionID == sessionID {
			select {
			case c.send <- data:
			default:
			}
		}
	}
}

// pushNotificationToSession delivers a notification to all live WS clients for a session.
func (s *Server) pushNotificationToSession(sessionID string, id int64, title, message, level string) {
	msg, _ := json.Marshal(map[string]interface{}{
		"type":      "notification",
		"id":        id,
		"title":     title,
		"message":   message,
		"level":     level,
		"read":      false,
		"createdAt": time.Now().Format(time.RFC3339),
	})
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if c.sessionID == sessionID {
			select {
			case c.send <- msg:
			default:
			}
		}
	}
}
