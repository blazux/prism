package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Embedder calls an embedding endpoint. With the default Ollama backend it hits
// /api/embed; with the "openai" backend (SGLang/vLLM/…) it hits /v1/embeddings.
type Embedder struct {
	backend string // "ollama" (default) or "openai"
	baseURL string // for openai: includes the /v1 suffix
	apiKey  string // optional, openai backend only
	model   string
	client  *http.Client
}

// NewEmbedder builds an Ollama-backed embedder (unchanged default path).
func NewEmbedder(ollamaURL, model string) *Embedder {
	return &Embedder{
		backend: "ollama",
		baseURL: ollamaURL,
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// NewOpenAIEmbedder builds an embedder against an OpenAI-compatible endpoint.
// baseURL must point at the /v1 root.
func NewOpenAIEmbedder(baseURL, apiKey, model string) *Embedder {
	return &Embedder{
		backend: "openai",
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// EmbedBatch embeds multiple texts in a single request. Both Ollama and the
// OpenAI API accept a list of inputs and return one embedding per entry.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if e.backend == "openai" {
		return e.embedOpenAI(ctx, texts)
	}

	body, err := json.Marshal(map[string]interface{}{
		"model": e.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embed", bytes.NewReader(body))
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

// embedOpenAI hits POST /v1/embeddings. The response carries embeddings under
// data[].embedding, indexed back to the input order via the "index" field.
func (e *Embedder) embedOpenAI(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model": e.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embed returned %d (model=%s): %s", resp.StatusCode, e.model, bytes.TrimSpace(body))
	}

	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Data))
	}
	out := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
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
