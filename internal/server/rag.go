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

	embedder := rag.NewEmbedder(s.cfg.OllamaURL, s.cfg.EmbedModel)

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
	ragInitStatus.Store("ready")
	log.Println("[rag] ready")
}

// registerRAGRoutes adds /api/rag/* handlers to the mux.
func (s *Server) registerRAGRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/rag/status", s.handleRAGStatus)
	mux.HandleFunc("/api/rag/collections", s.handleRAGCollections)
	mux.HandleFunc("/api/rag/documents", s.handleRAGDocuments)
	mux.HandleFunc("/api/rag/upload", s.handleRAGUpload)
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
func (s *Server) handleRAGCollections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.ragEnabled(w) {
		return
	}

	sessionID := sanitizeSessionID(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = "default"
	}

	switch r.Method {
	case http.MethodGet:
		cols, err := s.ragStore.ListCollections(r.Context(), sessionID)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cols == nil {
			cols = []rag.Collection{}
		}
		json.NewEncoder(w).Encode(cols)

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			jsonError(w, "missing name", http.StatusBadRequest)
			return
		}
		if err := s.ragStore.DeleteCollection(r.Context(), name); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodPatch:
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			jsonError(w, "invalid body (need name + description)", http.StatusBadRequest)
			return
		}
		if err := s.ragStore.SetCollectionDescription(r.Context(), body.Name, sessionID, body.Description); err != nil {
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

	uploadSession := sanitizeSessionID(r.FormValue("session"))
	if uploadSession == "" {
		uploadSession = "default"
	}

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

	// Parse text and build chunk→page mapping.
	// PPTX is converted to PDF first so we can reuse the page-aware pipeline.
	var chunks []string
	var pageNums []int
	ext := strings.ToLower(filepath.Ext(header.Filename))

	parsePath := tmp.Name() // may be replaced by converted PDF for PPTX
	var convertedDir string  // temp dir for LibreOffice output, cleaned up after

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
			jsonError(w, "parse: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		for pageIdx, pageText := range pages {
			for _, c := range rag.SplitText(pageText) {
				chunks = append(chunks, c)
				pageNums = append(pageNums, pageIdx+1)
			}
		}
	} else {
		text, err := rag.ParseFile(tmp.Name())
		if err != nil {
			jsonError(w, "parse: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		chunks = rag.SplitText(text)
		pageNums = make([]int, len(chunks))
	}

	if strings.TrimSpace(strings.Join(chunks, "")) == "" {
		jsonError(w, "no text extracted from file", http.StatusUnprocessableEntity)
		return
	}
	if len(chunks) == 0 {
		jsonError(w, "no chunks produced", http.StatusUnprocessableEntity)
		return
	}

	// Embed all chunks (batched, Ollama handles the full list)
	embeddings, err := s.ragEmbedder.EmbedBatch(r.Context(), chunks)
	if err != nil {
		jsonError(w, "embed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Extract page/slide images for PDF and PPTX (converted to PDF above).
	// For DOCX: extract embedded images directly from the ZIP.
	if s.cfg.WorkspaceDir != "" {
		imgDir := rag.ImageDir(s.cfg.WorkspaceDir, collection, header.Filename)
		origExt := strings.ToLower(filepath.Ext(header.Filename))
		if origExt == ".pdf" || origExt == ".pptx" {
			if err := rag.ExtractPageImages(parsePath, imgDir); err != nil {
				log.Printf("[rag] warn: could not extract images from %s: %v", header.Filename, err)
			}
		} else if origExt == ".docx" {
			if _, err := rag.ExtractDOCXImages(tmp.Name(), imgDir); err != nil {
				log.Printf("[rag] warn: could not extract docx images from %s: %v", header.Filename, err)
			}
		}
	}

	// Persist
	if err := s.ragStore.UpsertDocument(r.Context(), collection, header.Filename, fileHash, size, chunks, pageNums, embeddings); err != nil {
		jsonError(w, "store: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Register collection ownership for this session (no-op if already exists)
	_ = s.ragStore.EnsureCollection(r.Context(), collection, uploadSession)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"collection": collection,
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
