package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *Client) ModelCapabilities(ctx context.Context, model string) (ModelCapabilities, error) {
	return CachedCapabilities(ctx, fmt.Sprintf("ollama\x00%s\x00%s\x00%d", c.baseURL, model, NumCtx), func(ctx context.Context) (ModelCapabilities, error) {
		body, _ := json.Marshal(map[string]string{"model": model})
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/show", bytes.NewReader(body))
		if err != nil {
			return ModelCapabilities{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return ModelCapabilities{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ModelCapabilities{}, fmt.Errorf("model metadata unavailable (%d)", resp.StatusCode)
		}
		var result struct {
			ModelInfo    map[string]any `json:"model_info"`
			Capabilities []string       `json:"capabilities"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
			return ModelCapabilities{}, err
		}
		out := ModelCapabilities{ContextWindow: NumCtx}
		for k, value := range result.ModelInfo {
			if n, ok := value.(float64); ok && strings.HasSuffix(k, ".context_length") && n >= 4096 && n < float64(out.ContextWindow) {
				out.ContextWindow = int(n)
			}
		}
		if len(result.Capabilities) > 0 {
			v := false
			for _, cap := range result.Capabilities {
				if cap == "vision" {
					v = true
				}
			}
			out.Vision = &v
		}
		return out, nil
	})
}
