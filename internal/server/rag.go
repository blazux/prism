package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"prism/internal/agent"
	"strconv"
	"strings"
	"time"

	"prism/internal/rag"
)

const maxUploadSize = 50 << 20 // 50 MB

// ragContextFn returns the system-prompt callback that lists the RAG
// collections visible under scope, or nil when RAG is unavailable. Single
// source for every chat path (WS dashboard, headless/API) — the wording used
// to live copy-pasted in each caller and the headless copy silently lost the
// "don't guess, search first" sentence.
func (s *Server) ragContextFn(scope string) func() string {
	return func() string {
		ragStore, _, _, release := s.acquireRAG()
		defer release()
		if ragStore == nil {
			return ""
		}
		if s.ragPersonalFallbackBlocked(scope) {
			return ""
		}
		cols, err := ragStore.ListCollections(context.Background(), scope)
		if err != nil {
			return ""
		}
		// Prism's own docs live outside tenant scoping (one read-only copy):
		// list them too, so the agent knows the collection exists without the
		// prompt having to hard-code its name.
		var help *rag.Collection
		if hc, herr := ragStore.ListCollections(context.Background(), agent.HelpCollectionScope); herr == nil {
			for i := range hc {
				if hc[i].Name == agent.HelpCollection {
					help = &hc[i]
				}
			}
		}
		if len(cols) == 0 && help == nil {
			return ""
		}
		var sb strings.Builder
		sb.WriteString("## Knowledge Base (RAG)\n\n")
		sb.WriteString(agent.RAGCollectionGuidance + "\n\n")
		for _, c := range cols {
			name := unscopeCollection(scope, c.Name)
			if c.Description != "" {
				fmt.Fprintf(&sb, "- **%s** — %s (%d docs)\n", name, c.Description, c.DocCount)
			} else {
				fmt.Fprintf(&sb, "- **%s** (%d docs, %d chunks)\n", name, c.DocCount, c.ChunkCount)
			}
		}
		if help != nil {
			fmt.Fprintf(&sb, "- **%s** — Prism's own user documentation (%d docs, read-only): search it, or call prism_help, when the user asks how to use or configure Prism\n", agent.HelpCollection, help.DocCount)
		}
		return sb.String()
	}
}

// initRAG starts the same embedding configuration worker used by settings.
// Runs in a background goroutine — RAG endpoints return 503 until ready.
func (s *Server) initRAG(ctx context.Context) {
	if s.cfg.PostgresURL == "" {
		s.ragInitStatus.Store("disabled: POSTGRES_URL not set")
		return
	}
	for s.store() == nil {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	s.scheduleRAGApply()
}

// registerRAGRoutes adds /api/rag/* handlers to the mux.
func (s *Server) registerRAGRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/rag/status", s.resourceRoute((*Server).handleRAGStatus))
	mux.HandleFunc("/api/rag/collections", s.resourceRoute((*Server).handleRAGCollections))
	mux.HandleFunc("/api/rag/documents", s.resourceRoute((*Server).handleRAGDocuments))
	mux.HandleFunc("/api/rag/upload", s.resourceRoute((*Server).handleRAGUpload))
	mux.HandleFunc("/api/rag/upload/progress", s.resourceRoute((*Server).handleRAGUploadProgress))
	mux.HandleFunc("/api/rag/document", s.resourceRoute((*Server).handleRAGDocument))
}

// GET /api/rag/status — always returns 200, safe to poll from frontend
func (s *Server) handleRAGStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status, _ := s.ragInitStatus.Load().(string)
	store, _, _, release := s.acquireRAG()
	ready := store != nil
	release()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ready":    ready,
		"applying": s.ragUpdating.Load(),
		"status":   status,
	})
}

// /api/rag/collections  — GET list | DELETE ?name=x | PATCH (body: {name, description})
// ragScopeForRequest resolves the RAG scope for a request. An explicit
// ?group=<id> targets that group's knowledge base — reserved to its group
// admins (and global admins): it's how the admin console manages any group's
// RAG, mirroring /api/group/mcp. Without the param, the caller's own scope.
// A ?group= request is always management-grade, so no further canManage check.
func (s *Server) ragScopeForRequest(r *http.Request) (scope string, manage, ok bool) {
	// Reserved scope: the phone switchboard's own dedicated knowledge base.
	// Global admins manage it here; it is what an unknown caller's rag_search reads
	// (voice.go voiceGuestScope). Not tied to any group or user.
	if r.URL.Query().Get("scope") == voiceGuestScope {
		if u := currentUser(r); u != nil && u.IsGlobalAdmin() {
			return voiceGuestScope, true, true
		}
		return "", false, false
	}
	if g := r.URL.Query().Get("group"); g != "" {
		gid, err := strconv.ParseInt(g, 10, 64)
		if err != nil || gid <= 0 {
			return "", false, false
		}
		u := currentUser(r)
		if u == nil || !s.isGroupAdminOf(r.Context(), u, gid) {
			return "", false, false
		}
		return fmt.Sprintf("g%d", gid), true, true
	}
	u := currentUser(r)
	scope = s.ragScopeFor(r.Context(), u)
	if s.ragPersonalFallbackBlocked(scope) {
		return "", false, false
	}
	return scope, s.canManageRAGScope(r.Context(), u), true
}

func (s *Server) handleRAGCollections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	release, ok := s.lockRAGRequest(w)
	if !ok {
		return
	}
	defer release()

	// RAG is scoped to the user's group (or personal scope); collection names are
	// stored prefixed so tenants never collide (Phase 3b). ?group=<id> lets a
	// group admin manage that group's base from the admin console.
	scope, canManage, scopeOK := s.ragScopeForRequest(r)
	if !scopeOK {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// The group's knowledge base: only this scope's collections.
		cols, err := s.ragStore.ListCollections(r.Context(), scope)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cols == nil {
			cols = []rag.Collection{}
		}
		for i := range cols {
			cols[i].Name = unscopeCollection(scope, cols[i].Name)
		}
		json.NewEncoder(w).Encode(cols)

	case http.MethodDelete:
		if !canManage {
			jsonError(w, "the group knowledge base is managed by your group admin", http.StatusForbidden)
			return
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			jsonError(w, "missing name", http.StatusBadRequest)
			return
		}
		if err := s.ragStore.DeleteCollection(r.Context(), scopeCollection(scope, name)); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodPatch:
		if !canManage {
			jsonError(w, "the group knowledge base is managed by your group admin", http.StatusForbidden)
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			jsonError(w, "invalid body (need name + description)", http.StatusBadRequest)
			return
		}
		if err := s.ragStore.SetCollectionDescription(r.Context(), scopeCollection(scope, body.Name), scope, body.Description); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET /api/rag/documents?collection=xxx
func (s *Server) handleRAGDocuments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	release, ok := s.lockRAGRequest(w)
	if !ok {
		return
	}
	defer release()
	collection := r.URL.Query().Get("collection")
	if collection == "" {
		jsonError(w, "missing collection", http.StatusBadRequest)
		return
	}
	docScope, _, docOK := s.ragScopeForRequest(r)
	if !docOK {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	collection = scopeCollection(docScope, collection)
	docs, err := s.ragStore.ListDocuments(r.Context(), collection)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if docs == nil {
		docs = []rag.Document{}
	}
	json.NewEncoder(w).Encode(docs)
}

// DELETE /api/rag/document?id=xxx[&group=<id>]
//
// The id is global (rag_documents.id), so the delete is restricted to the
// caller's resolved scope: a group admin can only remove documents whose
// collection carries their group's prefix — never another tenant's by guessing
// an id. ?group=<id> targets that group's base from the admin console, exactly
// like the collections endpoint.
func (s *Server) handleRAGDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE only", http.StatusMethodNotAllowed)
		return
	}
	release, ok := s.lockRAGRequest(w)
	if !ok {
		return
	}
	defer release()
	scope, canManage, scopeOK := s.ragScopeForRequest(r)
	if !scopeOK || !canManage {
		jsonError(w, "the group knowledge base is managed by your group admin", http.StatusForbidden)
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	deleted, err := s.ragStore.DeleteDocumentInScope(r.Context(), id, ragScopePrefix(scope))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !deleted {
		jsonError(w, "document not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ragScopePrefix is the collection-name prefix every collection of a scope
// carries (see agent.ScopeCollection); empty for the unscoped legacy mode where
// names are stored bare.
func ragScopePrefix(scope string) string {
	if scope == "" {
		return ""
	}
	return scope + "--"
}

// POST /api/rag/upload  (multipart: file + collection)
func (s *Server) handleRAGUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	release, ok := s.lockRAGRequest(w)
	if !ok {
		return
	}
	defer release()
	upScope, upManage, upOK2 := s.ragScopeForRequest(r)
	if !upOK2 || !upManage {
		jsonError(w, "the group knowledge base is managed by your group admin", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		jsonError(w, "file too large (max 50 MB)", http.StatusBadRequest)
		return
	}

	collection := strings.TrimSpace(r.FormValue("collection"))
	if collection == "" {
		jsonError(w, "collection name required", http.StatusBadRequest)
		return
	}
	// Sanitize collection name
	collection = sanitizeName(collection)
	displayCol := collection
	if ms := s.store(); ms != nil {
		uid := int64(0)
		if u := currentUser(r); u != nil {
			uid = u.ID
		}
		ms.AddUsage(r.Context(), uid, "", "rag_upload", scopeCollection(upScope, displayCol), 1, nil)
	}

	_, upOK := s.sessionFor(r, r.FormValue("session"))
	if !upOK {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	// Scope the collection so tenants never collide (Phase 3b); ?group=<id>
	// (admin console) targets that group's base.
	scope := upScope
	collection = scopeCollection(scope, collection)

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Save to temp file for parsing
	tmp, err := os.CreateTemp("", "rag-upload-*"+filepath.Ext(header.Filename))
	if err != nil {
		jsonError(w, "temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp.Name())

	hasher := sha256.New()
	mw := io.MultiWriter(tmp, hasher)
	size, err := io.Copy(mw, file)
	tmp.Close()
	if err != nil {
		jsonError(w, "write temp: "+err.Error(), http.StatusInternalServerError)
		return
	}
	fileHash := fmt.Sprintf("%x", hasher.Sum(nil))

	// Ingestion is synchronous and can take minutes on a large manual; without
	// these lines the server said nothing at all while the browser waited.
	started := time.Now()
	log.Printf("[rag] ingest %q into %q: %.1f MB — parsing…", header.Filename, displayCol, float64(size)/(1<<20))
	// Publish progress so the UI can poll it while its own POST is in flight.
	s.ingest.set(collection, header.Filename, ingestProgress{Stage: "parsing"})
	fail := func(stage string, err error) {
		log.Printf("[rag] ingest %q FAILED at %s: %v", header.Filename, stage, err)
		s.ingest.set(collection, header.Filename, ingestProgress{Stage: "failed", Error: err.Error()})
	}

	// Parse text and build chunk→page mapping.
	// PPTX is converted to PDF first so we can reuse the page-aware pipeline.
	var chunks []string
	var pageNums []int
	ext := strings.ToLower(filepath.Ext(header.Filename))

	parsePath := tmp.Name() // may be replaced by converted PDF for PPTX
	var convertedDir string // temp dir for LibreOffice output, cleaned up after

	if ext == ".pptx" {
		convertedDir, err = os.MkdirTemp("", "pptx-convert-*")
		if err != nil {
			jsonError(w, "temp dir: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(convertedDir)
		pdfPath, err := rag.ConvertToPDF(tmp.Name(), convertedDir)
		if err != nil {
			jsonError(w, "pptx→pdf: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		parsePath = pdfPath
		ext = ".pdf"
	}

	if ext == ".pdf" {
		pages, err := rag.ParsePDFPages(parsePath)
		if err != nil {
			fail("parse", err)
			jsonError(w, "parse: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		// Split the document as a whole (not page by page) so paragraphs running
		// across a page break stay together; each chunk keeps the page it starts on.
		chunks, pageNums = rag.SplitPages(pages)
	} else {
		text, err := rag.ParseFile(tmp.Name())
		if err != nil {
			fail("parse", err)
			jsonError(w, "parse: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		chunks, pageNums = rag.SplitDocument(text)
	}

	if strings.TrimSpace(strings.Join(chunks, "")) == "" {
		jsonError(w, "no text extracted from file", http.StatusUnprocessableEntity)
		return
	}
	if len(chunks) == 0 {
		jsonError(w, "no chunks produced", http.StatusUnprocessableEntity)
		return
	}
	parsed := time.Now()
	log.Printf("[rag] ingest %q: parsed in %s → %d chunks; embedding…",
		header.Filename, parsed.Sub(started).Round(time.Millisecond), len(chunks))
	s.ingest.set(collection, header.Filename, ingestProgress{Stage: "embedding", Total: len(chunks)})

	// Embedded in slices: a whole manual in one request never returns in time.
	// The ETA comes from the measured rate, so it is honest from the first slice.
	embedStart := time.Now()
	logged := embedStart
	embeddings, err := s.ragEmbedder.EmbedBatchProgress(r.Context(), chunks, func(done, total int) {
		elapsed := time.Since(embedStart)
		rate := float64(done) / elapsed.Seconds()
		eta := time.Duration(float64(elapsed) / float64(done) * float64(total-done))
		// The UI reads every slice; the log is throttled so it stays readable.
		s.ingest.set(collection, header.Filename, ingestProgress{
			Stage: "embedding", Done: done, Total: total,
			Rate: rate, ETASecs: int(eta.Seconds() + 0.5),
		})
		if time.Since(logged) < 5*time.Second && done < total {
			return
		}
		logged = time.Now()
		log.Printf("[rag] ingest %q: embedded %d/%d chunks (%.0f/s), ~%s left",
			header.Filename, done, total, rate, eta.Round(time.Second))
	})
	if err != nil {
		fail("embedding", err)
		jsonError(w, "embed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Extract page/slide images for PDF and PPTX (converted to PDF above).
	// Persist
	log.Printf("[rag] ingest %q: embedded %d chunks in %s; storing…",
		header.Filename, len(chunks), time.Since(embedStart).Round(time.Second))
	s.ingest.set(collection, header.Filename, ingestProgress{Stage: "storing", Done: len(chunks), Total: len(chunks)})
	if err := s.ragStore.UpsertDocument(r.Context(), collection, header.Filename, fileHash, size, chunks, pageNums, embeddings); err != nil {
		fail("store", err)
		jsonError(w, "store: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.ingest.set(collection, header.Filename, ingestProgress{Stage: "done", Done: len(chunks), Total: len(chunks)})
	log.Printf("[rag] ingest %q into %q: done — %d chunks in %s",
		header.Filename, displayCol, len(chunks), time.Since(started).Round(time.Second))
	// Register collection ownership for this scope (no-op if already exists)
	_ = s.ragStore.EnsureCollection(r.Context(), collection, scope)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"collection": displayCol,
		"filename":   header.Filename,
		"chunks":     len(chunks),
		"size":       size,
	})
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
