package rag

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// widgetPrompt asks the vision model to critique a rendered dashboard widget so
// a text-only chat model can verify it without sight. Unlike captionPrompt it
// always describes something (never NONE).
const widgetPrompt = `You are inspecting a screenshot of a rendered dashboard widget (an HTML/JS panel). In 2-4 sentences, describe what is actually visible, then explicitly flag any rendering problems: broken or empty layout, missing or broken images/icons, overlapping or clipped text, blank/white areas, visible error messages, or content overflowing its frame. If everything renders cleanly, say so plainly. Report only what you see.`

// Captioner generates text descriptions of document page images using the
// vision capabilities of the main chat model. With the default Ollama backend
// it hits /api/chat; with the "openai" backend (SGLang/vLLM/…) it hits
// /v1/chat/completions with the image inlined as a data: URL.
type Captioner struct {
	backend string // "ollama" (default) or "openai"
	baseURL string // for openai: includes the /v1 suffix
	apiKey  string // optional, openai backend only
	model   string
	client  *http.Client
}

func NewCaptioner(ollamaURL, model string) *Captioner {
	return &Captioner{
		backend: "ollama",
		baseURL: ollamaURL,
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// NewOpenAICaptioner builds a captioner against an OpenAI-compatible endpoint.
// baseURL must point at the /v1 root.
func NewOpenAICaptioner(baseURL, apiKey, model string) *Captioner {
	return &Captioner{
		backend: "openai",
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// describe sends an image to the vision model with the given prompt and returns
// the raw model text (trimmed).
func (c *Captioner) describe(ctx context.Context, imagePath, prompt string) (string, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString(data)

	var url string
	var body []byte
	if c.backend == "openai" {
		url = c.baseURL + "/chat/completions"
		body, err = json.Marshal(map[string]interface{}{
			"model":  c.model,
			"stream": false,
			"messages": []map[string]interface{}{{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + b64}},
				},
			}},
		})
	} else {
		url = c.baseURL + "/api/chat"
		body, err = json.Marshal(map[string]interface{}{
			"model":  c.model,
			"stream": false,
			"messages": []map[string]interface{}{{
				"role":    "user",
				"content": prompt,
				"images":  []string{b64},
			}},
		})
	}
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.backend == "openai" && c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("caption chat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("caption chat returned %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}

	var content string
	if c.backend == "openai" {
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", fmt.Errorf("decode chat response: %w", err)
		}
		if len(result.Choices) > 0 {
			content = result.Choices[0].Message.Content
		}
	} else {
		var result struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", fmt.Errorf("decode chat response: %w", err)
		}
		content = result.Message.Content
	}

	return strings.TrimSpace(content), nil
}

// DescribeWidget asks the vision model to critique a rendered dashboard-widget
// screenshot so a text-only chat model can verify its own widget without sight.
func (c *Captioner) DescribeWidget(ctx context.Context, imagePath string) (string, error) {
	return c.describe(ctx, imagePath, widgetPrompt)
}
