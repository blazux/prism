package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/rag"
)

// ragBlockedMsg is returned by the RAG tools when MULTI_USER retires the
// personal fallback scope (no group) — see ToolExecutor.ragBlocked.
const ragBlockedMsg = "No knowledge base available — you're not in a group; ask your admin to add you to one."

func (e *ToolExecutor) ragSearch(ctx context.Context, query, collection string, limit int) (string, []string, error) {
	if e.ragBlocked() {
		return ragBlockedMsg, nil, nil
	}
	if e.ragStore == nil || e.ragEmbedder == nil {
		return "RAG not available (Postgres not configured)", nil, nil
	}
	if query == "" || collection == "" {
		return "", nil, fmt.Errorf("query and collection are required")
	}

	embedding, err := e.ragEmbedder.Embed(ctx, query)
	if err != nil {
		return fmt.Sprintf("ERROR embedding query: %v", err), nil, nil
	}

	results, err := e.ragStore.Search(ctx, e.resolveCollection(collection), embedding, limit)
	if err != nil {
		return fmt.Sprintf("ERROR searching: %v", err), nil, nil
	}
	if len(results) == 0 {
		// Distinguish "collection exists but matched nothing" from "no such
		// collection" (a guessed/misspelled name) — the latter otherwise reads
		// as an empty knowledge base and dead-ends the agent on a mandated search.
		if cols, cerr := e.ragStore.ListCollections(ctx, e.ragScope); cerr == nil {
			want := e.resolveCollection(collection)
			found := false
			var names []string
			for _, c := range cols {
				names = append(names, e.uncol(c.Name))
				if c.Name == want {
					found = true
				}
			}
			if !found {
				if len(names) == 0 {
					return fmt.Sprintf("Collection %q does not exist — there are no collections yet (use rag_ingest to create one).", collection), nil, nil
				}
				return fmt.Sprintf("Collection %q does not exist. Available collections: %s (rag_manage action=list shows them). Re-run rag_search against the right name.", collection, strings.Join(names, ", ")), nil, nil
			}
		}
		return fmt.Sprintf("No results found in collection %q for query: %s", collection, query), nil, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d relevant chunks in collection %q:\n\n", len(results), collection)
	for i, r := range results {
		pageInfo := ""
		if r.PageNumber > 0 {
			pageInfo = fmt.Sprintf(", page %d", r.PageNumber)
		}
		fmt.Fprintf(&sb, "--- [%d] %s (chunk %d%s, score %.3f) ---\n%s\n\n", i+1, r.Filename, r.ChunkIndex, pageInfo, r.Score, r.Content)
	}

	return sb.String(), nil, nil
}

func (e *ToolExecutor) ragIngest(ctx context.Context, collection, source, content, sourcePath string) (string, error) {
	if e.ragBlocked() {
		return ragBlockedMsg, nil
	}
	if collection == HelpCollection {
		return helpReadOnlyMsg, nil
	}
	if e.ragStore == nil || e.ragEmbedder == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	// Scope the collection to this tenant; keep the plain name for messages.
	displayCol := collection
	scope := e.resolveScope(collection)
	collection = e.resolveCollection(collection)

	var chunks []string
	var pageNums []int
	var fileHash string
	var sizeBytes int64

	if sourcePath != "" {
		// File-based ingestion — resolve and safety-check the path.
		sourcePath = NormalizeWorkspacePath(sourcePath) // absorb an echoed /workspace/ prefix — else it double-joins
		fullPath := filepath.Join(e.workspaceDir, sourcePath)
		rel, err := filepath.Rel(e.workspaceDir, fullPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("source_path escapes workspace")
		}

		if st, serr := os.Stat(fullPath); serr == nil && st.IsDir() {
			// A folder is the natural first guess; teach the one-file-per-call rule
			// with the actual file names instead of a bare "is a directory".
			entries, _ := os.ReadDir(fullPath)
			var names []string
			for _, en := range entries {
				if !en.IsDir() {
					names = append(names, en.Name())
				}
			}
			if len(names) == 0 {
				return fmt.Sprintf("%s is a directory with no files in it — rag_ingest takes one file per call.", sourcePath), nil
			}
			return fmt.Sprintf("%s is a directory — rag_ingest takes ONE file per call. Files in it: %s. Ingest them one at a time into the same collection.", sourcePath, strings.Join(names, ", ")), nil
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				return e.notFoundHint(sourcePath, fullPath).Error(), nil // teach: siblings + closest name
			}
			return fmt.Sprintf("ERROR reading file: %v", err), nil
		}
		sum := sha256.Sum256(data)
		fileHash = fmt.Sprintf("%x", sum)
		sizeBytes = int64(len(data))

		if source == "" {
			source = filepath.Base(fullPath)
		}

		fileExt := strings.ToLower(filepath.Ext(fullPath))

		parsePath := fullPath
		var convertedDir string

		if fileExt == ".pptx" {
			var err error
			convertedDir, err = os.MkdirTemp("", "pptx-convert-*")
			if err != nil {
				return fmt.Sprintf("ERROR creating temp dir: %v", err), nil
			}
			defer os.RemoveAll(convertedDir)
			pdfPath, err := rag.ConvertToPDF(fullPath, convertedDir)
			if err != nil {
				return fmt.Sprintf("ERROR converting PPTX to PDF: %v", err), nil
			}
			parsePath = pdfPath
			fileExt = ".pdf"
		}

		if fileExt == ".pdf" {
			pages, err := rag.ParsePDFPages(parsePath)
			if err != nil {
				return fmt.Sprintf("ERROR parsing PDF: %v", err), nil
			}
			// Whole-document split: a paragraph across a page break stays whole,
			// and each chunk still reports the page it starts on.
			chunks, pageNums = rag.SplitPages(pages)
		} else if fileExt == ".docx" {
			text, err := rag.ParseFile(fullPath)
			if err != nil {
				return fmt.Sprintf("ERROR parsing DOCX: %v", err), nil
			}
			chunks, pageNums = rag.SplitDocument(text)
		} else {
			text, err := rag.ParseFile(fullPath)
			if err != nil {
				return fmt.Sprintf("ERROR parsing file: %v", err), nil
			}
			chunks, pageNums = rag.SplitDocument(text)
		}
	} else {
		// Inline text ingestion.
		if source == "" || content == "" {
			return "", fmt.Errorf("source and content are required when source_path is not provided")
		}
		chunks, pageNums = rag.SplitDocument(content)
		sizeBytes = int64(len(content))
	}

	if len(chunks) == 0 {
		return "Content produced no chunks after splitting.", nil
	}

	if err := e.ragStore.EnsureCollection(ctx, collection, scope); err != nil {
		return fmt.Sprintf("ERROR registering collection: %v", err), nil
	}

	// Same slicing + progress as the upload path: a large manual would otherwise be
	// one opaque request, and nothing would say where the ingestion stands.
	embedStart := time.Now()
	logged := embedStart
	embeddings, err := e.ragEmbedder.EmbedBatchProgress(ctx, chunks, func(done, total int) {
		if time.Since(logged) < 5*time.Second && done < total {
			return
		}
		logged = time.Now()
		elapsed := time.Since(embedStart)
		eta := time.Duration(float64(elapsed) / float64(done) * float64(total-done))
		log.Printf("[rag] ingest %q: embedded %d/%d chunks (%.0f/s), ~%s left",
			source, done, total, float64(done)/elapsed.Seconds(), eta.Round(time.Second))
	})
	if err != nil {
		log.Printf("[rag] ingest %q FAILED at embedding: %v", source, err)
		return fmt.Sprintf("ERROR embedding content: %v", err), nil
	}
	log.Printf("[rag] ingest %q: %d chunks embedded in %s", source, len(chunks), time.Since(embedStart).Round(time.Second))

	if err := e.ragStore.UpsertDocument(ctx, collection, source, fileHash, sizeBytes, chunks, pageNums, embeddings); err != nil {
		return fmt.Sprintf("ERROR storing document: %v", err), nil
	}

	return fmt.Sprintf("Ingested %q into collection %q: %d chunks indexed.", source, displayCol, len(chunks)), nil
}

func (e *ToolExecutor) ragListCollections(ctx context.Context) (string, error) {
	if e.ragBlocked() {
		return ragBlockedMsg, nil
	}
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}

	cols, err := e.ragStore.ListCollections(ctx, e.ragScope)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	help := e.helpCollectionInfo(ctx)
	if len(cols) == 0 && help == nil {
		return "No collections yet. Use rag_ingest to create one.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "RAG collections (%d):\n\n", len(cols))
	for _, c := range cols {
		fmt.Fprintf(&sb, "  • %s — %d docs, %d chunks", e.uncol(c.Name), c.DocCount, c.ChunkCount)
		if c.Description != "" {
			fmt.Fprintf(&sb, "\n    %s", c.Description)
		}
		sb.WriteByte('\n')
	}
	if help != nil {
		fmt.Fprintf(&sb, "  • %s — Prism's own user documentation, %d docs (read-only; search it, or call prism_help, for how-to questions)\n", HelpCollection, help.DocCount)
	}
	return sb.String(), nil
}

func (e *ToolExecutor) ragListDocuments(ctx context.Context, collection string) (string, error) {
	if e.ragBlocked() {
		return ragBlockedMsg, nil
	}
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	displayCol := collection
	collection = e.resolveCollection(collection)

	docs, err := e.ragStore.ListDocuments(ctx, collection)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	if len(docs) == 0 {
		return fmt.Sprintf("No documents in collection %q.", displayCol), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Documents in %q (%d):\n\n", displayCol, len(docs))
	for _, d := range docs {
		fmt.Fprintf(&sb, "  • %s — %d chunks, %d bytes, updated %s\n",
			d.Filename, d.ChunkCount, d.SizeBytes, d.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return sb.String(), nil
}

func (e *ToolExecutor) ragDelete(ctx context.Context, collection, document string) (string, error) {
	if e.ragBlocked() {
		return ragBlockedMsg, nil
	}
	if collection == HelpCollection {
		return helpReadOnlyMsg, nil
	}
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	displayCol := collection
	collection = e.resolveCollection(collection)

	if document == "" {
		// Verify the collection exists so a typo'd name reports honestly instead
		// of a fake "deleted" — else the agent's model of the KB silently diverges.
		if cols, cerr := e.ragStore.ListCollections(ctx, e.ragScope); cerr == nil {
			found := false
			var names []string
			for _, c := range cols {
				names = append(names, e.uncol(c.Name))
				if c.Name == collection {
					found = true
				}
			}
			if !found {
				if len(names) == 0 {
					return fmt.Sprintf("Collection %q does not exist — nothing deleted (no collections yet).", displayCol), nil
				}
				return fmt.Sprintf("Collection %q does not exist — nothing deleted. Available: %s.", displayCol, strings.Join(names, ", ")), nil
			}
		}
		if err := e.ragStore.DeleteCollection(ctx, collection); err != nil {
			return fmt.Sprintf("ERROR: %v", err), nil
		}
		return fmt.Sprintf("Collection %q deleted", displayCol), nil
	}

	docs, err := e.ragStore.ListDocuments(ctx, collection)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	for _, d := range docs {
		if d.Filename == document {
			if err := e.ragStore.DeleteDocument(ctx, d.ID); err != nil {
				return fmt.Sprintf("ERROR: %v", err), nil
			}
			return fmt.Sprintf("Document %q deleted from collection %q", document, displayCol), nil
		}
	}
	var names []string
	for _, d := range docs {
		names = append(names, d.Filename)
	}
	if len(names) == 0 {
		return fmt.Sprintf("Document %q not found — collection %q is empty.", document, displayCol), nil
	}
	return fmt.Sprintf("Document %q not found in collection %q. Documents there: %s", document, displayCol, strings.Join(names, ", ")), nil
}

// resolveCollection maps a user-facing collection name to its storage name.
// agent-learnings and user-profile are always personal (pcol/personalScope),
// even when called through the generic rag_search/rag_ingest/rag_delete
// tools — save_learning and save_user_info always write there, so an agent
// that falls back to a manual rag_search on "agent-learnings" (e.g. after the
// automatic per-turn lookup came back empty) must land on the same physical
// collection, not the group's same-named one via col()/ragScope.
func (e *ToolExecutor) resolveCollection(name string) string {
	if name == learningsCollection || name == userProfileCollection {
		return e.pcol(name)
	}
	if name == HelpCollection {
		return HelpCollection // one deployment-wide copy, outside tenant scoping
	}
	return e.col(name)
}

// resolveScope is resolveCollection's scope counterpart, for EnsureCollection.
func (e *ToolExecutor) resolveScope(name string) string {
	if name == learningsCollection || name == userProfileCollection {
		return e.personalScope()
	}
	if name == HelpCollection {
		return HelpCollectionScope
	}
	return e.ragScope
}

// ─── Learnings tool ───────────────────────────────────────────────────────────

const learningsCollection = "agent-learnings"

func (e *ToolExecutor) saveLearning(ctx context.Context, title, content string) (string, error) {
	if e.ragStore == nil || e.ragEmbedder == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if title == "" || content == "" {
		return "", fmt.Errorf("title and content are required")
	}

	if err := e.ragStore.EnsureCollection(ctx, e.pcol(learningsCollection), e.personalScope()); err != nil {
		return fmt.Sprintf("ERROR registering collection: %v", err), nil
	}

	full := title + "\n\n" + content
	chunks := rag.SplitText(full)
	if len(chunks) == 0 {
		return "Content produced no chunks after splitting.", nil
	}

	embeddings, err := e.ragEmbedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return fmt.Sprintf("ERROR embedding content: %v", err), nil
	}

	pageNums := make([]int, len(chunks))
	if err := e.ragStore.UpsertDocument(ctx, e.pcol(learningsCollection), title, "", int64(len(full)), chunks, pageNums, embeddings); err != nil {
		return fmt.Sprintf("ERROR storing learning: %v", err), nil
	}

	return fmt.Sprintf("Learning %q saved to agent-learnings (%d chunks).", title, len(chunks)), nil
}

// SearchLearnings queries the agent-learnings collection and returns a formatted
// string suitable for injection into the system prompt. Returns "" if nothing relevant.
func (e *ToolExecutor) SearchLearnings(ctx context.Context, query string) string {
	if e.ragStore == nil || e.ragEmbedder == nil || query == "" {
		return ""
	}

	embedding, err := e.ragEmbedder.Embed(ctx, query)
	if err != nil {
		return ""
	}

	results, err := e.ragStore.Search(ctx, e.pcol(learningsCollection), embedding, 3)
	if err != nil || len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, r := range results {
		if r.Score < 0.55 {
			continue
		}
		fmt.Fprintf(&sb, "### %s\n%s\n\n", r.Filename, r.Content)
	}
	return sb.String()
}

// ─── User profile tool ────────────────────────────────────────────────────────

const userProfileCollection = "user-profile"

func (e *ToolExecutor) saveUserInfo(ctx context.Context, topic, content string) (string, error) {
	if e.ragStore == nil || e.ragEmbedder == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if topic == "" || content == "" {
		return "", fmt.Errorf("topic and content are required")
	}

	if err := e.ragStore.EnsureCollection(ctx, e.pcol(userProfileCollection), e.personalScope()); err != nil {
		return fmt.Sprintf("ERROR registering collection: %v", err), nil
	}

	full := topic + "\n\n" + content
	chunks := rag.SplitText(full)
	if len(chunks) == 0 {
		return "Content produced no chunks after splitting.", nil
	}

	embeddings, err := e.ragEmbedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return fmt.Sprintf("ERROR embedding content: %v", err), nil
	}

	pageNums := make([]int, len(chunks))
	if err := e.ragStore.UpsertDocument(ctx, e.pcol(userProfileCollection), topic, "", int64(len(full)), chunks, pageNums, embeddings); err != nil {
		return fmt.Sprintf("ERROR storing user info: %v", err), nil
	}

	return fmt.Sprintf("User info %q saved to profile.", topic), nil
}

// GetUserProfile retrieves the full user profile from the user-profile collection.
// The profile is always injected in full (no semantic search) since it's small
// and potentially relevant to any conversation.
func (e *ToolExecutor) GetUserProfile(ctx context.Context) string {
	if e.ragStore == nil {
		return ""
	}

	chunks, err := e.ragStore.AllContent(ctx, e.pcol(userProfileCollection))
	if err != nil || len(chunks) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString(c)
		sb.WriteString("\n")
	}
	return sb.String()
}

// ─── Notification tool ────────────────────────────────────────────────────────
