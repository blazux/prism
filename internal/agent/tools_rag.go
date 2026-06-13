package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/rag"
)

func (e *ToolExecutor) ragSearch(ctx context.Context, query, collection string, limit int) (string, []string, error) {
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

	results, err := e.ragStore.Search(ctx, collection, embedding, limit)
	if err != nil {
		return fmt.Sprintf("ERROR searching: %v", err), nil, nil
	}
	if len(results) == 0 {
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
	if e.ragStore == nil || e.ragEmbedder == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}

	var chunks []string
	var pageNums []int
	var fileHash string
	var sizeBytes int64
	var visualPages []int // PDF pages selected for background figure captioning
	var imgDir string

	if sourcePath != "" {
		// File-based ingestion — resolve and safety-check the path.
		fullPath := filepath.Join(e.workspaceDir, sourcePath)
		rel, err := filepath.Rel(e.workspaceDir, fullPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("source_path escapes workspace")
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Sprintf("ERROR reading file: %v", err), nil
		}
		sum := sha256.Sum256(data)
		fileHash = fmt.Sprintf("%x", sum)
		sizeBytes = int64(len(data))

		if source == "" {
			source = filepath.Base(fullPath)
		}

		imgDir = rag.ImageDir(e.workspaceDir, collection, source)
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
			for pageIdx, pageText := range pages {
				for _, c := range rag.SplitText(pageText) {
					chunks = append(chunks, c)
					pageNums = append(pageNums, pageIdx+1)
				}
			}
			if err := rag.ExtractPageImages(parsePath, imgDir); err != nil {
				fmt.Printf("WARN: could not extract page images from %s: %v\n", fullPath, err)
			} else {
				// Keep only pages likely to contain figures; must run while
				// parsePath (possibly a temp PPTX→PDF conversion) still exists.
				visualPages = rag.PlanVisualPages(parsePath, imgDir, pages)
			}
		} else if fileExt == ".docx" {
			text, err := rag.ParseFile(fullPath)
			if err != nil {
				return fmt.Sprintf("ERROR parsing DOCX: %v", err), nil
			}
			chunks = rag.SplitText(text)
			pageNums = make([]int, len(chunks))
			if _, err := rag.ExtractDOCXImages(fullPath, imgDir); err != nil {
				fmt.Printf("WARN: could not extract DOCX images from %s: %v\n", fullPath, err)
			}
		} else {
			text, err := rag.ParseFile(fullPath)
			if err != nil {
				return fmt.Sprintf("ERROR parsing file: %v", err), nil
			}
			chunks = rag.SplitText(text)
			pageNums = make([]int, len(chunks))
		}
	} else {
		// Inline text ingestion.
		if source == "" || content == "" {
			return "", fmt.Errorf("source and content are required when source_path is not provided")
		}
		chunks = rag.SplitText(content)
		pageNums = make([]int, len(chunks))
		sizeBytes = int64(len(content))
	}

	if len(chunks) == 0 {
		return "Content produced no chunks after splitting.", nil
	}

	if err := e.ragStore.EnsureCollection(ctx, collection, e.sessionID); err != nil {
		return fmt.Sprintf("ERROR registering collection: %v", err), nil
	}

	embeddings, err := e.ragEmbedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return fmt.Sprintf("ERROR embedding content: %v", err), nil
	}

	if err := e.ragStore.UpsertDocument(ctx, collection, source, fileHash, sizeBytes, chunks, pageNums, embeddings); err != nil {
		return fmt.Sprintf("ERROR storing document: %v", err), nil
	}

	if e.ragCaptioner != nil && len(visualPages) > 0 {
		go e.captionFiguresBackground(collection, source, imgDir, visualPages)
		return fmt.Sprintf("Ingested %q into collection %q: %d chunks indexed. %d page(s) look visual — figure captioning runs in the background and will add searchable [Figure — page N] chunks (a notification is sent when done).",
			source, collection, len(chunks), len(visualPages)), nil
	}
	return fmt.Sprintf("Ingested %q into collection %q: %d chunks indexed.", source, collection, len(chunks)), nil
}

// captionFiguresBackground captions visual pages with the vision model and
// notifies the dashboard when done. Runs detached from the request context.
func (e *ToolExecutor) captionFiguresBackground(collection, source, imgDir string, pages []int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	n, err := rag.CaptionPages(ctx, e.ragStore, e.ragEmbedder, e.ragCaptioner, imgDir, collection, source, pages)
	if err != nil {
		fmt.Printf("WARN: figure captioning for %s/%s: %v\n", collection, source, err)
		if e.onNotification != nil {
			e.onNotification("RAG", fmt.Sprintf("Figure captioning failed for %q: %v", source, err), "warning")
		}
		return
	}
	if e.onNotification != nil {
		e.onNotification("RAG", fmt.Sprintf("%d figure(s) from %q are now searchable in collection %q.", n, source, collection), "success")
	}
}

func (e *ToolExecutor) ragShowPage(ctx context.Context, collection, filename string, page int) (string, []string, error) {
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil, nil
	}
	if collection == "" || filename == "" || page < 1 {
		return "", nil, fmt.Errorf("collection, filename and page are required")
	}

	doc, err := e.ragStore.FindDocument(ctx, collection, filename)
	if err != nil {
		return fmt.Sprintf("ERROR looking up document: %v", err), nil, nil
	}
	if doc == nil {
		return fmt.Sprintf("Document %q not found in collection %q.", filename, collection), nil, nil
	}

	// Use glob to support both .jpg and .png (DOCX images may be PNG).
	imgDir := rag.ImageDir(e.workspaceDir, collection, filename)
	matches, _ := filepath.Glob(filepath.Join(imgDir, fmt.Sprintf("page-%04d.*", page)))
	if len(matches) == 0 {
		return fmt.Sprintf("No image available for page/image %d of %q (the document may not have been ingested with image extraction).", page, filename), nil, nil
	}
	imgPath := matches[0]
	data, err := os.ReadFile(imgPath)
	if err != nil {
		return fmt.Sprintf("No image available for page/image %d of %q.", page, filename), nil, nil
	}

	attachPath := filepath.ToSlash(rag.PageImagePath("", collection, filename, page))
	return fmt.Sprintf("Page %d of %q — call add_attachment(%q) to also include it in your response.", page, filename, attachPath), []string{base64.StdEncoding.EncodeToString(data)}, nil
}

func (e *ToolExecutor) ragListCollections(ctx context.Context) (string, error) {
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}

	cols, err := e.ragStore.ListCollections(ctx, e.sessionID)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	if len(cols) == 0 {
		return "No collections yet. Use rag_ingest to create one.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "RAG collections (%d):\n\n", len(cols))
	for _, c := range cols {
		fmt.Fprintf(&sb, "  • %s — %d docs, %d chunks", c.Name, c.DocCount, c.ChunkCount)
		if c.Description != "" {
			fmt.Fprintf(&sb, "\n    %s", c.Description)
		}
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func (e *ToolExecutor) ragListDocuments(ctx context.Context, collection string) (string, error) {
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}

	docs, err := e.ragStore.ListDocuments(ctx, collection)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	if len(docs) == 0 {
		return fmt.Sprintf("No documents in collection %q.", collection), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Documents in %q (%d):\n\n", collection, len(docs))
	for _, d := range docs {
		fmt.Fprintf(&sb, "  • %s — %d chunks, %d bytes, updated %s\n",
			d.Filename, d.ChunkCount, d.SizeBytes, d.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return sb.String(), nil
}

func (e *ToolExecutor) ragDelete(ctx context.Context, collection, document string) (string, error) {
	if e.ragStore == nil {
		return "RAG not available (Postgres not configured)", nil
	}
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}

	if document == "" {
		if err := e.ragStore.DeleteCollection(ctx, collection); err != nil {
			return fmt.Sprintf("ERROR: %v", err), nil
		}
		return fmt.Sprintf("Collection %q deleted", collection), nil
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
			return fmt.Sprintf("Document %q deleted from collection %q", document, collection), nil
		}
	}
	return fmt.Sprintf("Document %q not found in collection %q", document, collection), nil
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

	if err := e.ragStore.EnsureCollection(ctx, learningsCollection, "default"); err != nil {
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
	if err := e.ragStore.UpsertDocument(ctx, learningsCollection, title, "", int64(len(full)), chunks, pageNums, embeddings); err != nil {
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

	results, err := e.ragStore.Search(ctx, learningsCollection, embedding, 3)
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

	if err := e.ragStore.EnsureCollection(ctx, userProfileCollection, "default"); err != nil {
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
	if err := e.ragStore.UpsertDocument(ctx, userProfileCollection, topic, "", int64(len(full)), chunks, pageNums, embeddings); err != nil {
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

	chunks, err := e.ragStore.AllContent(ctx, userProfileCollection)
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
