package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Conversation summaries belong to an explicitly selected chat, not Settings.
func (s *Server) handleConversationContext(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("session") == "" {
		writeErr(w, 400, "conversation required")
		return
	}
	session, ok := s.sessionFor(r, r.URL.Query().Get("session"))
	if !ok {
		writeErr(w, 403, "conversation access denied")
		return
	}
	u := currentUser(r)
	ms := s.store()
	if ms == nil {
		writeErr(w, 503, "conversation context unavailable")
		return
	}
	if r.Method == http.MethodGet {
		context, summary, err := ms.ReadContext(r.Context(), session)
		if err != nil {
			writeErr(w, 500, "could not read conversation context")
			return
		}
		archive := ""
		if context != nil {
			archive = summary
			summary = context.Summary
		}
		writeJSON(w, map[string]any{"session": session, "summary": summary, "archiveSummary": archive})
		return
	}
	if r.Method != http.MethodPatch {
		writeErr(w, 405, "GET or PATCH required")
		return
	}
	var b struct{ Kind, Content, Original string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&b) != nil || len(b.Content) > 128<<10 {
		writeErr(w, 400, "invalid context correction")
		return
	}
	if b.Kind == "context" || b.Kind == "archive" {
		if r.Method != http.MethodPatch || strings.TrimSpace(b.Content) == "" {
			writeErr(w, 400, "a non-empty context correction is required")
			return
		}
		s.runsMu.Lock()
		defer s.runsMu.Unlock()
		if s.runs[runKey(&Client{user: u, sessionID: session})] != nil || len(s.sessionJobs[session]) > 0 {
			writeErr(w, 409, "wait until this conversation's task finishes")
			return
		}
		lc, _, err := ms.ReadContext(r.Context(), session)
		if err != nil {
			writeErr(w, 500, "could not read conversation context")
			return
		}
		if lc == nil || b.Kind == "archive" {
			if b.Original == "" {
				writeErr(w, 409, "no context summary to correct")
				return
			}
			changed, err := ms.CorrectSummary(r.Context(), session, b.Original, b.Content)
			if err != nil {
				writeErr(w, 500, "could not save context")
				return
			}
			if !changed {
				writeErr(w, 409, "the context changed; reload it before editing")
				return
			}
		} else {
			if lc.Summary != b.Original {
				writeErr(w, 409, "the context changed; reload it before editing")
				return
			}
			changed, err := ms.CorrectLiveContext(r.Context(), session, *lc, b.Content)
			if err != nil {
				writeErr(w, 500, "could not save context")
				return
			}
			if !changed {
				writeErr(w, 409, "the context changed; reload it before editing")
				return
			}
		}
		s.mu.RLock()
		for client := range s.clients {
			if client.sessionID == session {
				client.mu.Lock()
				client.historyStale = true
				client.mu.Unlock()
			}
		}
		s.mu.RUnlock()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	writeErr(w, 400, "context or archive correction required")
}
