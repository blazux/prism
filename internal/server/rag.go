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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"prism/internal/rag"
)

const maxUploadSize = 50 << 20 // 50 MB

var ragInitStatus atomic.Value // stores string

// initRAG initialises the embedder and store, retrying until success.
// Runs in a background goroutine — RAG endpoints return 503 until ready.
func (s *Server) initRAG(ctx context.Context) {
	ragInitStatus.Store("initializing")

	if s.cfg.PostgresURL == "" {
		ragInitStatus.Store("disabled: POSTGRES_URL not set")
		log.Println("[rag] POSTGRES_URL not set — RAG disabled")
		return
	}
	if s.cfg.EmbedModel == "" {
		ragInitStatus.Store("disabled: EMBED_MODEL not set")
		log.Println("[rag] EMBED_MODEL not set — RAG disabled")
		return
	}

	embedder := s.newEmbedder()

	// Probe embedding dimension (retry — model may need to load)
	var dim int
	for attempt := 1; ; attempt++ {
		probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		ragInitStatus.Store(fmt.Sprintf("probing embed model (attempt %d)…", attempt))
		log.Printf("[rag] probing embedding dim for %s (attempt %d)…", s.cfg.EmbedModel, attempt)
		var err error
		dim, err = embedder.Dim(probeCtx)
		cancel()
		if err == nil {
			break
		}
		log.Printf("[rag] embed probe failed: %v — retrying in 5s", err)
		ragInitStatus.Store(fmt.Sprintf("embed probe failed: %v — retrying…", err))
		select {
		case <-ctx.Done():
			ragInitStatus.Store("cancelled")
			return
		case <-time.After(5 * time.Second):
		}
	}
	log.Printf("[rag] embedding dim=%d", dim)

	// Connect to Postgres (retry — container may still be starting)
	var store *rag.Store
	for attempt := 1; ; attempt++ {
		ragInitStatus.Store(fmt.Sprintf("connecting to postgres (attempt %d)…", attempt))
		log.Printf("[rag] connecting to postgres (attempt %d)…", attempt)
		var err error
		storeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		store, err = rag.NewStore(storeCtx, s.cfg.PostgresURL, dim)
		cancel()
		if err == nil {
			break
		}
		log.Printf("[rag] postgres failed: %v — retrying in 3s", err)
		ragInitStatus.Store(fmt.Sprintf("postgres: %v — retrying…", err))
		select {
		case <-ctx.Done():
			ragInitStatus.Store("cancelled")
			return
		case <-time.After(3 * time.Second):
		}
	}

	s.ragEmbedder = embedder
	s.ragStore = store
	s.ragCaptioner = s.newCaptioner()
	ragInitStatus.Store("ready")
	log.Println("[rag] ready")

	// Index Prism's bundled help docs for semantic search (idempotent).
	if docs, hash := s.loadHelpDocs(); len(docs) > 0 {
		s.ingestHelpDocs(ctx, docs, hash)
	}
}

// registerRAGRoutes adds /api/rag/* handlers to the mux.
func (s *Server) registerRAGRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/rag/status", s.handleRAGStatus)
	mux.HandleFunc("/api/rag/collections", s.handleRAGCollections)
	mux.HandleFunc("/api/rag/documents", s.handleRAGDocuments)
	mux.HandleFunc("/api/rag/upload", s.handleRAGUpload)
	mux.HandleFunc("/api/rag/upload/progress", s.handleRAGUploadProgress)
	mux.HandleFunc("/api/rag/document", s.handleRAGDocument)
}

// GET /api/rag/status — always returns 200, safe to poll from frontend
func (s *Server) handleRAGStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status, _ := ragInitStatus.Load().(string)
	ready := s.ragStore != nil
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ready":  ready,
		"status": status,
	})
}

func (s *Server) ragEnabled(w http.ResponseWriter) bool {
	if s.ragStore == nil {
		status, _ := ragInitStatus.Load().(string)
		jsonError(w, "RAG not ready: "+status, http.StatusServiceUnavailable)
		return false
	}
	return true
}

// /api/rag/collections  — GET list | DELETE ?name=x | PATCH (body: {name, description})
// ragScopeForRequest resolves the RAG scope for a request. An explicit
// ?group=<id> targets that group's knowledge base — reserved to its group
// admins (and global admins): it's how the admin console manages any group's
// RAG, mirroring /api/group/mcp. Without the param, the caller's own scope.
// A ?group= request is always management-grade, so no further canManage check.
func (s *Server) ragScopeForRequest(r *http.Request) (scope string, manage, ok bool) {
	// Reserved scope (Vortex): the phone switchboard's own dedicated knowledge base.
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
	if !s.ragEnabled(w) {
		return
	}

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
	if !s.ragEnabled(w) {
		return
	}
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

// DELETE /api/rag/document?id=xxx
func (s *Server) handleRAGDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE only", http.StatusMethodNotAllowed)
		return
	}
	if !s.ragEnabled(w) {
		return
	}
	if !s.canManageRAGScope(r.Context(), currentUser(r)) {
		jsonError(w, "the group knowledge base is managed by your group admin", http.StatusForbidden)
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.ragStore.DeleteDocument(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/rag/upload  (multipart: file + collection)
func (s *Server) handleRAGUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.ragEnabled(w) {
		return
	}
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
