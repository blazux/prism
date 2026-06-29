package server

// Prism's own documentation, bundled into the binary, made available to the
// agent so it can answer usage/capability/setup questions and walk users through
// connecting accounts. Two delivery paths for robustness:
//   - materialized to $WORKSPACE_DIR/.prism_help/ so the agent can read_file them
//     even when RAG is unavailable (no embed model, cold start);
//   - ingested into the "prism-help" RAG collection for semantic search when RAG
//     becomes ready (idempotent via a content hash).

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"prism/internal/rag"
)

const (
	helpEmbedDir    = "docs/help"
	helpCollection  = "prism-help"
	helpWorkspaceNS = ".prism_help"
	helpHashKey     = "help_docs_hash"
	helpSession     = "default"
)

// helpDoc is one bundled markdown file.
type helpDoc struct {
	name string // e.g. "overview.md"
	body string
}

func (s *Server) loadHelpDocs() ([]helpDoc, string) {
	var docs []helpDoc
	h := sha256.New()
	entries, err := fs.ReadDir(s.cfg.HelpFS, helpEmbedDir)
	if err != nil {
		return nil, ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // stable hash
	for _, name := range names {
		b, err := fs.ReadFile(s.cfg.HelpFS, helpEmbedDir+"/"+name)
		if err != nil {
			continue
		}
		docs = append(docs, helpDoc{name: name, body: string(b)})
		h.Write([]byte(name))
		h.Write(b)
	}
	return docs, fmt.Sprintf("%x", h.Sum(nil))
}

// materializeHelpDocs writes the bundled docs to the workspace so read_file works
// regardless of RAG. Cheap; called synchronously at startup.
func (s *Server) materializeHelpDocs(docs []helpDoc) {
	dir := filepath.Join(s.cfg.WorkspaceDir, helpWorkspaceNS)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[help] mkdir: %v", err)
		return
	}
	for _, d := range docs {
		if err := os.WriteFile(filepath.Join(dir, d.name), []byte(d.body), 0644); err != nil {
			log.Printf("[help] write %s: %v", d.name, err)
		}
	}
}

// ingestHelpDocs indexes the docs into the prism-help RAG collection if their
// content changed since last time. Safe to call once RAG is ready.
func (s *Server) ingestHelpDocs(ctx context.Context, docs []helpDoc, hash string) {
	if s.ragStore == nil || s.ragEmbedder == nil || len(docs) == 0 {
		return
	}
	if s.memStore != nil {
		if prev, ok, _ := s.memStore.GetConfig(ctx, helpHashKey); ok && prev == hash {
			return // already up to date
		}
	}
	if err := s.ragStore.EnsureCollection(ctx, helpCollection, helpSession); err != nil {
		log.Printf("[help] ensure collection: %v", err)
		return
	}
	for _, d := range docs {
		chunks := rag.SplitText(d.body)
		if len(chunks) == 0 {
			continue
		}
		pageNums := make([]int, len(chunks))
		embeddings, err := s.ragEmbedder.EmbedBatch(ctx, chunks)
		if err != nil {
			log.Printf("[help] embed %s: %v", d.name, err)
			return // try again next start
		}
		sum := sha256.Sum256([]byte(d.body))
		if err := s.ragStore.UpsertDocument(ctx, helpCollection, d.name, fmt.Sprintf("%x", sum), int64(len(d.body)), chunks, pageNums, embeddings); err != nil {
			log.Printf("[help] upsert %s: %v", d.name, err)
			return
		}
	}
	if s.memStore != nil {
		s.memStore.SetConfig(ctx, helpHashKey, hash)
	}
	log.Printf("[help] indexed %d doc(s) into %q", len(docs), helpCollection)
}
