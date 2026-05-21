package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Embedder calls Ollama's /api/embed endpoint.
type Embedder struct {
	ollamaURL string
	model     string
	client    *http.Client
}

func NewEmbedder(ollamaURL, model string) *Embedder {
	return &Embedder{
		ollamaURL: ollamaURL,
		model:     model,
		client:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// EmbedBatch embeds multiple texts in a single Ollama request.
// Ollama accepts input as a list and returns one embedding per entry.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model": e.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.ollamaURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed returned %d (model=%s): %s", resp.StatusCode, e.model, bytes.TrimSpace(body))
	}

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Embeddings))
	}
	return result.Embeddings, nil
}

// Embed embeds a single text.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// Dim probes the embedding dimension by embedding a short test string.
func (e *Embedder) Dim(ctx context.Context) (int, error) {
	v, err := e.Embed(ctx, "dim")
	if err != nil {
		return 0, err
	}
	if len(v) == 0 {
		return 0, fmt.Errorf("empty embedding")
	}
	return len(v), nil
}
