package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"prism/internal/rag"
)

// The personal memory pane uses the same scopes and embedding provider as the
// agent. It never substitutes the user's shared knowledge-base scope.
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u := currentUser(r)
	scope := ""
	if s.cfg.MultiUser {
		if u == nil || u.ID <= 0 {
			writeErr(w, 403, "personal identity required")
			return
		}
		scope = fmt.Sprintf("u%d", u.ID)
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, 503, "memory unavailable")
		return
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("kind") == "" {
		store, _, _, release := s.acquireRAG()
		defer release()
		result := map[string]any{"scope": scope, "profile": []rag.Document{}, "learnings": []rag.Document{}, "knowledgeAvailable": store != nil}
		if store != nil {
			for kind, col := range map[string]string{"profile": "user-profile", "learnings": "agent-learnings"} {
				docs, err := store.ListDocuments(r.Context(), scopeCollection(scope, col))
				if err != nil {
					writeErr(w, 500, "could not read saved memory")
					return
				}
				if docs != nil {
					result[kind] = docs
				}
			}
		}
		writeJSON(w, result)
		return
	}
	var b struct{ Kind, Name, Content, Revision string }
	if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&b) != nil {
			writeErr(w, 400, "invalid memory change")
			return
		}
		if len(b.Content) > 128<<10 {
			writeErr(w, 400, "memory text exceeds 128 KB")
			return
		}
	} else if r.Method == http.MethodGet {
		b.Kind = r.URL.Query().Get("kind")
		b.Name = r.URL.Query().Get("name")
	} else {
		writeErr(w, 405, "GET, PATCH or DELETE required")
		return
	}
	col := map[string]string{"profile": "user-profile", "learnings": "agent-learnings"}[b.Kind]
	if col == "" || b.Name == "" {
		writeErr(w, 400, "memory kind and name required")
		return
	}
	collection := scopeCollection(scope, col)
	store, embedder, _, release := s.acquireRAG()
	defer release()
	if store == nil {
		writeErr(w, 503, "saved memory unavailable")
		return
	}
	doc, err := store.FindDocument(r.Context(), collection, b.Name)
	if err != nil {
		writeErr(w, 500, "could not read memory")
		return
	}
	if doc == nil {
		writeErr(w, 404, "memory not found")
		return
	}
	text, err := store.DocumentText(r.Context(), collection, b.Name)
	if err != nil {
		writeErr(w, 500, "could not read memory text")
		return
	}
	revision := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]any{"content": text, "revision": revision, "truncated": doc.ChunkCount > 100 || len(text) > 128<<10})
		return
	}
	if b.Revision != revision {
		writeErr(w, 409, "memory changed; reload it before editing")
		return
	}
	if r.Method == http.MethodDelete {
		err = store.DeleteDocument(r.Context(), doc.ID)
	} else {
		if doc.ChunkCount > 100 || len(text) > 128<<10 {
			writeErr(w, 400, "this memory is too long to edit here")
			return
		}
		if embedder == nil || strings.TrimSpace(b.Content) == "" {
			writeErr(w, 400, "non-empty text and an embedding provider are required")
			return
		}
		chunks := rag.SplitText(b.Content)
		vectors, embedErr := embedder.EmbedBatch(r.Context(), chunks)
		if embedErr != nil {
			writeErr(w, 502, "could not index the correction; the previous memory was kept")
			return
		}
		err = store.CorrectDocument(r.Context(), *doc, b.Content, chunks, vectors)
	}
	if errors.Is(err, rag.ErrDocumentChanged) {
		writeErr(w, 409, "memory changed while indexing; reload it before editing")
		return
	}
	if err != nil {
		writeErr(w, 500, "could not save memory")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
