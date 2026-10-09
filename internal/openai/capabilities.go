package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"prism/internal/ollama"
	"strings"
)

func (c *Client) ModelCapabilities(ctx context.Context, model string) (ollama.ModelCapabilities, error) {
	return ollama.CachedCapabilities(ctx, "openai\x00"+c.baseURL+"\x00"+c.apiKey+"\x00"+model, func(ctx context.Context) (ollama.ModelCapabilities, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/models", nil)
		if err != nil {
			return ollama.ModelCapabilities{}, err
		}
		c.auth(req)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return ollama.ModelCapabilities{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ollama.ModelCapabilities{}, fmt.Errorf("model metadata unavailable (%d)", resp.StatusCode)
		}
		var body struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
			return ollama.ModelCapabilities{}, err
		}
		for _, m := range body.Data {
			if m["id"] == model {
				info := ollama.ParseCapabilities(m)
				if info.ContextWindow == 0 || info.Vision == nil {
					// LiteLLM's OpenAI model list contains IDs only. Its public
					// group endpoint exposes capabilities, without deployment keys.
					// Probe only the configured gateway, never its upstream servers.
					if extra, err := c.modelGroupCapabilities(ctx, model); err == nil {
						if info.ContextWindow == 0 {
							info.ContextWindow = extra.ContextWindow
						}
						if info.Vision == nil {
							info.Vision = extra.Vision
						}
					}
				}
				return info, nil
			}
		}
		return ollama.ModelCapabilities{}, nil
	})
}

func (c *Client) modelGroupCapabilities(ctx context.Context, model string) (ollama.ModelCapabilities, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil || strings.EqualFold(u.Hostname(), "api.openai.com") {
		return ollama.ModelCapabilities{}, fmt.Errorf("gateway metadata unavailable")
	}
	// Keep any reverse-proxy prefix, including when hosted below /gateway/v1.
	u.Path = strings.TrimSuffix(u.Path, "/v1") + "/model_group/info"
	u.RawPath = ""
	u.RawQuery = url.Values{"model_group": {model}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ollama.ModelCapabilities{}, err
	}
	c.auth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ollama.ModelCapabilities{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ollama.ModelCapabilities{}, fmt.Errorf("gateway metadata unavailable (%d)", resp.StatusCode)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return ollama.ModelCapabilities{}, err
	}
	for _, m := range body.Data {
		if m["model_group"] == model {
			info := ollama.ParseCapabilities(m)
			// LiteLLM ModelGroupInfo defaults missing/null supports_vision
			// to false. This endpoint cannot distinguish unknown from an
			// explicit opt-out, so its negative value must not disable images.
			// Explicit /models metadata and manual overrides still honor false.
			if info.Vision != nil && !*info.Vision {
				info.Vision = nil
			}
			return info, nil
		}
	}
	return ollama.ModelCapabilities{}, nil
}
