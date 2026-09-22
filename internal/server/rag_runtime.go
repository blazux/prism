package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"prism/internal/rag"
)

// A lease covers the entire embed + query/write operation. Reconfiguration
// drains existing leases before migrating vectors and replacing the pair.
// New calls fail promptly during migration instead of queuing behind a long job.
func (s *Server) acquireRAG() (*rag.Store, *rag.Embedder, *rag.Captioner, func()) {
	if !s.ragMu.TryRLock() {
		return nil, nil, nil, func() {}
	}
	if s.ragUpdating.Load() {
		s.ragMu.RUnlock()
		return nil, nil, nil, func() {}
	}
	return s.ragStore, s.ragEmbedder, s.ragCaptioner, s.ragMu.RUnlock
}
func (s *Server) lockRAGRequest(w http.ResponseWriter) (func(), bool) {
	store, _, _, release := s.acquireRAG()
	if store == nil {
		release()
		writeErr(w, http.StatusServiceUnavailable, "Document search is unavailable or being reconfigured. Check AI provider status and retry.")
		return func() {}, false
	}
	return release, true
}
func (s *Server) ragCaptionerFor(p *aiProfile) *rag.Captioner {
	if p.ServerDefaults || p.ChatVision == nil {
		return s.newCaptioner()
	}
	if !*p.ChatVision {
		return nil
	}
	return rag.NewBackendCaptioner(p.backendConfig().newChatBackend(), p.Model)
}

// The worker always reads the latest saved profile. New saves cancel obsolete
// probes/rebuilds; the index transaction rolls back before the next job starts.
func (s *Server) scheduleRAGApply() {
	if s.cfg.PostgresURL == "" {
		return
	}
	s.mu.Lock()
	s.ragGeneration++
	generation := s.ragGeneration
	if s.ragCancel != nil {
		s.ragCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.ragCancel = cancel
	s.ragUpdating.Store(true)
	ragInitStatus.Store("applying configuration: waiting for active document operations…")
	s.mu.Unlock()
	go func() {
		s.ragApplyMu.Lock()
		defer s.ragApplyMu.Unlock()
		defer cancel()
		if ctx.Err() != nil {
			return
		}
		err := s.applyRAGProfile(ctx)
		s.mu.Lock()
		current := generation == s.ragGeneration
		if current {
			s.ragUpdating.Store(false)
			if err != nil {
				ragInitStatus.Store("configuration failed; previous document index retained. Check the provider and save again.")
				log.Printf("[rag] apply failed: %v", err)
			}
		}
		s.mu.Unlock()
		if current && err == nil {
			if docs, hash := s.loadHelpDocs(); len(docs) > 0 {
				s.ingestHelpDocs(ctx, docs, hash)
			}
		}
	}()
}
func (s *Server) applyRAGProfile(ctx context.Context) error {
	p, err := loadAIProfile(ctx, s.store())
	if err != nil {
		return err
	}
	legacy := s.environmentAIProfile()
	if p == nil {
		p = legacy
		p.ServerDefaults = true
	} else if p.ServerDefaults {
		confirmation := p.Embedding
		p = legacy
		p.ServerDefaults = true
		if confirmation != nil && confirmation.ReindexFor == embeddingIdentity(p.effectiveEmbedding()) {
			p.Embedding.Reindex = confirmation.Reindex
			p.Embedding.ReindexFor = confirmation.ReindexFor
		}
	}
	if p.Embedding == nil {
		p.Embedding = legacy.Embedding
	}
	// Chat-only changes do not contact the embedding provider or rebuild its pool.
	s.mu.Lock()
	active := s.activeEmbedding
	s.mu.Unlock()
	if active != nil && sameEmbedding(*active, *p) {
		s.ragMu.Lock()
		defer s.ragMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		s.ragCaptioner = s.ragCaptionerFor(p)
		s.mu.Lock()
		s.activeEmbedding = p
		s.mu.Unlock()
		if s.ragStore == nil {
			ragInitStatus.Store("disabled: no embedding model configured")
		} else {
			ragInitStatus.Store("ready")
		}
		return nil
	}
	ep := p.effectiveEmbedding()
	// Probe without interrupting in-flight requests. Their existing credentials
	// and embedder remain immutable until all operations have completed.
	var embedder *rag.Embedder
	dim := 0
	if ep != nil && ep.Model != "" {
		embedder = ep.embedder()
		for {
			probe, cancel := context.WithTimeout(ctx, 60*time.Second)
			ragInitStatus.Store("testing embedding connection…")
			dim, err = embedder.Dim(probe)
			cancel()
			if err == nil {
				break
			}
			if active != nil {
				return fmt.Errorf("embedding probe failed")
			}
			// At startup a local model server may still be loading. A new Save
			// cancels this retry immediately and applies the latest profile.
			ragInitStatus.Store("embedding connection unavailable; retrying…")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
	s.ragMu.Lock()
	defer s.ragMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var store *rag.Store
	if embedder != nil {
		ragInitStatus.Store("checking document index…")
		// Open the replacement pool before committing the index migration, so a
		// pool/schema failure cannot leave live callers with incompatible vectors.
		store, err = rag.NewMigrationStore(ctx, s.cfg.PostgresURL, dim)
		if err != nil {
			return err
		}
		err = rag.PrepareEmbeddingIndex(ctx, s.cfg.PostgresURL, embedder, dim, embeddingIdentity(ep), embeddingIdentity(legacy.effectiveEmbedding()), p.Embedding.Reindex && p.Embedding.ReindexFor == embeddingIdentity(ep), func(done int) { ragInitStatus.Store(fmt.Sprintf("rebuilding document index: %d chunks…", done)) })
		if err != nil {
			store.Close()
			return err
		}
	}
	old := s.ragStore
	s.ragStore, s.ragEmbedder, s.ragCaptioner = store, embedder, s.ragCaptionerFor(p)
	s.mu.Lock()
	s.activeEmbedding = p
	s.mu.Unlock()
	if store == nil {
		ragInitStatus.Store("disabled: no embedding model configured")
	} else {
		ragInitStatus.Store("ready")
	}
	if old != nil {
		old.Close()
	}
	return nil
}
