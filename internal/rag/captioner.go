package rag

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// captionPrompt asks the vision model to describe visual content only, and to
// answer NONE for pages without any, so they can be discarded.
const captionPrompt = `Describe the figures, diagrams, charts, schematics and tables on this document page, densely and factually, so the description can be found by semantic search. Include labels, axis names, component names and relationships shown. Ignore plain paragraphs of text. If the page contains no figure, diagram, chart, schematic or table, reply with exactly: NONE`

// Captioner generates text descriptions of document page images using the
// vision capabilities of the main Ollama chat model.
type Captioner struct {
	ollamaURL string
	model     string
	client    *http.Client
}

func NewCaptioner(ollamaURL, model string) *Captioner {
	return &Captioner{
		ollamaURL: ollamaURL,
		model:     model,
		client:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// CaptionPage sends a page image to the vision model and returns its
// description. Returns "" (no error) when the model reports no visual content.
func (c *Captioner) CaptionPage(ctx context.Context, imagePath string) (string, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(map[string]interface{}{
		"model":  c.model,
		"stream": false,
		"messages": []map[string]interface{}{{
			"role":    "user",
			"content": captionPrompt,
			"images":  []string{base64.StdEncoding.EncodeToString(data)},
		}},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.ollamaURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama chat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama chat returned %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}

	caption := strings.TrimSpace(result.Message.Content)
	if caption == "" || strings.EqualFold(caption, "NONE") || strings.EqualFold(caption, "NONE.") {
		return "", nil
	}
	return caption, nil
}

// PlanVisualPages selects the rendered pages likely to contain visual content
// and deletes the page images of all other pages. Must be called synchronously
// after ExtractPageImages, while pdfPath still exists.
//
// A page is a candidate when pdfimages reports raster images on it, or when its
// extracted text is short (vector-drawn schematics carry little text).
// pageTexts[i] is the text of page i+1; pages beyond len(pageTexts) are
// candidates by default (pdftotext drops trailing empty pages).
func PlanVisualPages(pdfPath, imgDir string, pageTexts []string) []int {
	imagePages := listImagePages(pdfPath)

	matches, err := filepath.Glob(filepath.Join(imgDir, "page-*.jpg"))
	if err != nil || len(matches) == 0 {
		return nil
	}

	var candidates []int
	for _, imgPath := range matches {
		var page int
		if _, err := fmt.Sscanf(filepath.Base(imgPath), "page-%04d.jpg", &page); err != nil || page < 1 {
			continue
		}
		text := ""
		if page <= len(pageTexts) {
			text = pageTexts[page-1]
		}
		if imagePages[page] || len(strings.TrimSpace(text)) < 200 {
			candidates = append(candidates, page)
			continue
		}
		os.Remove(imgPath)
	}
	return candidates
}

// CaptionPages captions the candidate pages sequentially, embeds the captions
// and appends them as searchable chunks to the document. Page images whose
// caption comes back empty (no visual content) are deleted. Returns the number
// of pages that produced a caption. Intended to run in a background goroutine.
func CaptionPages(ctx context.Context, store *Store, embedder *Embedder, captioner *Captioner, imgDir, collection, filename string, candidates []int) (int, error) {
	var captions []string
	var pages []int
	for _, page := range candidates {
		imgPath := filepath.Join(imgDir, fmt.Sprintf("page-%04d.jpg", page))
		caption, err := captioner.CaptionPage(ctx, imgPath)
		if err != nil {
			log.Printf("[rag] caption page %d of %s: %v", page, filename, err)
			continue // keep the image: rag_show_page still works
		}
		if caption == "" {
			os.Remove(imgPath)
			continue
		}
		captions = append(captions, fmt.Sprintf("[Figure — page %d] %s", page, caption))
		pages = append(pages, page)
	}
	if len(captions) == 0 {
		return 0, nil
	}

	embeddings, err := embedder.EmbedBatch(ctx, captions)
	if err != nil {
		return 0, fmt.Errorf("embed captions: %w", err)
	}
	if err := store.AppendChunks(ctx, collection, filename, captions, pages, embeddings); err != nil {
		return 0, fmt.Errorf("append caption chunks: %w", err)
	}
	return len(captions), nil
}

// listImagePages parses `pdfimages -list` output and returns the set of pages
// containing at least one raster image.
func listImagePages(pdfPath string) map[int]bool {
	out, err := execCommandOutput("pdfimages", "-list", pdfPath)
	if err != nil {
		return nil
	}
	pages := make(map[int]bool)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		var page int
		if _, err := fmt.Sscanf(fields[0], "%d", &page); err == nil && page > 0 {
			pages[page] = true
		}
	}
	return pages
}
