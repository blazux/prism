package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"prism/internal/ollama"
)

func (c *Client) ModelCapabilities(ctx context.Context, model string) (ollama.ModelCapabilities, error) {
	return ollama.CachedCapabilities(ctx, "anthropic\x00"+c.baseURL+"\x00"+c.apiKey+"\x00"+model, func(ctx context.Context) (ollama.ModelCapabilities, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/models/"+url.PathEscape(model), nil)
		if err != nil {
			return ollama.ModelCapabilities{}, err
		}
		if err := c.auth(req); err != nil {
			return ollama.ModelCapabilities{}, err
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return ollama.ModelCapabilities{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ollama.ModelCapabilities{}, fmt.Errorf("model metadata unavailable (%d)", resp.StatusCode)
		}
		var m map[string]any
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&m); err != nil {
			return ollama.ModelCapabilities{}, err
		}
		return ollama.ParseCapabilities(m), nil
	})
}
