package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Tool is an MCP tool definition as returned by tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client sends JSON-RPC 2.0 requests to an MCP server over HTTP.
//
// Two transports are supported and auto-detected during Initialize:
//   - Streamable HTTP (2025-03-26 spec): single POST endpoint, optional Mcp-Session-Id.
//   - Legacy HTTP+SSE (2024-11-05 spec): GET /sse → endpoint event → POST /messages.
//
// Detection: Streamable HTTP is tried first. If the server returns HTTP 404 or 405
// (endpoint not found / method not allowed), legacy SSE is attempted automatically.
type Client struct {
	baseURL     string
	authHeader  string
	sessionID   string // Mcp-Session-Id for Streamable HTTP
	messagesURL string // resolved messages endpoint for legacy SSE (empty = Streamable)
	http        *http.Client
	seq         atomic.Int64
}

func NewClient(baseURL, authHeader string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authHeader: authHeader,
		http:       &http.Client{Timeout: 30 * time.Second},
	}
}

// ─── JSON-RPC plumbing ────────────────────────────────────────────────────────

type rpcReq struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int64      `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// postURL returns the URL to POST JSON-RPC requests to.
// setAuth updates the Authorization header sent on subsequent requests, so a cached
// client picks up a refreshed OAuth token.
func (c *Client) setAuth(header string) { c.authHeader = header }

func (c *Client) postURL() string {
	if c.messagesURL != "" {
		return c.messagesURL
	}
	return c.baseURL
}

// call sends a JSON-RPC request and returns the result.
func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.seq.Add(1)
	body, _ := json.Marshal(rpcReq{JSONRPC: "2.0", ID: &id, Method: method, Params: params})

	req, err := http.NewRequestWithContext(ctx, "POST", c.postURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	// Capture session ID returned by Streamable HTTP servers.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	if resp.StatusCode == http.StatusUnauthorized {
		// The server wants OAuth. Surface a typed error carrying the resource_metadata
		// pointer so the manager can run discovery + the authorization flow.
		rm, _ := resourceMetadataFromChallenge(resp.Header.Get("WWW-Authenticate"))
		io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return nil, &OAuthRequiredError{ResourceMetadata: rm}
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	// Legacy SSE: POST to /messages returns 202 Accepted — response comes via SSE stream.
	// Most servers also return 200 with a body (handled below), but some strict
	// implementations only use the SSE channel.
	if resp.StatusCode == 202 {
		return nil, fmt.Errorf("server returned 202 Accepted — this server requires a persistent SSE channel for responses, which is not supported in stateless mode")
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return readSSEResult(resp.Body, id)
	}

	var r rpcResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", r.Error.Code, r.Error.Message)
	}
	return r.Result, nil
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(ctx context.Context, method string, params interface{}) {
	body, _ := json.Marshal(rpcReq{JSONRPC: "2.0", Method: method, Params: params})
	req, err := http.NewRequestWithContext(ctx, "POST", c.postURL(), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Streamable HTTP requires Accept on every POST, notifications included: a
	// strict server answers 406 and never marks the session initialized. FastMCP
	// does exactly that (observed on RTClient).
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.http.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func readSSEResult(r io.Reader, id int64) (json.RawMessage, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "" {
			continue
		}
		var resp rpcResp
		if json.Unmarshal([]byte(data), &resp) != nil {
			continue
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
	return nil, fmt.Errorf("SSE stream ended without matching response for id %d", id)
}

// ─── Transport detection ──────────────────────────────────────────────────────

// isEndpointNotFound returns true when the error indicates the POST endpoint
// doesn't exist (404/405) and we should try legacy SSE as a fallback.
func isEndpointNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.HasPrefix(s, "HTTP 404") ||
		strings.HasPrefix(s, "HTTP 405") ||
		strings.HasPrefix(s, "HTTP 301") ||
		strings.HasPrefix(s, "HTTP 302") ||
		strings.HasPrefix(s, "HTTP 307") ||
		strings.HasPrefix(s, "HTTP 308")
}

// discoverSSEEndpoint tries to find a legacy SSE endpoint at the given URL.
// It connects, reads SSE events until it sees an "endpoint" event, and returns
// the resolved messages URL.
func (c *Client) discoverSSEEndpoint(ctx context.Context, sseURL string) (string, error) {
	// Use a short timeout just to receive the endpoint event.
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(tctx, "GET", sseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}

	// Use a separate client without the global timeout for streaming.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		return "", fmt.Errorf("not an SSE endpoint (Content-Type: %s)", ct)
	}

	// Read SSE events until we get the endpoint event.
	scanner := bufio.NewScanner(resp.Body)
	var eventType, data string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(line[5:])
		case line == "":
			if eventType == "endpoint" && data != "" {
				return resolveRef(sseURL, data), nil
			}
			eventType, data = "", ""
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("SSE read: %w", err)
	}
	return "", fmt.Errorf("SSE stream closed without endpoint event")
}

// resolveRef resolves ref (possibly relative) against base.
func resolveRef(base, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

// tryLegacySSE attempts to discover a legacy SSE transport (2024-11-05 spec).
// It tries the base URL and the base URL + "/sse" as SSE endpoints.
func (c *Client) tryLegacySSE(ctx context.Context) error {
	candidates := []string{c.baseURL, c.baseURL + "/sse"}

	var lastErr error
	for _, sseURL := range candidates {
		messagesURL, err := c.discoverSSEEndpoint(ctx, sseURL)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", sseURL, err)
			continue
		}
		c.messagesURL = messagesURL
		return nil
	}
	return fmt.Errorf("legacy SSE not found: %w", lastErr)
}

// ─── Public API ───────────────────────────────────────────────────────────────

// Initialize performs the MCP protocol handshake.
// It auto-detects the transport: Streamable HTTP is tried first; if the server
// returns HTTP 404 or 405, legacy SSE (GET /sse + POST /messages) is tried.
func (c *Client) Initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "prism", "version": "1.0"},
	}

	// Try Streamable HTTP first (POST to base URL).
	_, err := c.call(ctx, "initialize", params)
	if err == nil {
		// Streamable HTTP: notify and done.
		c.notify(ctx, "notifications/initialized", nil)
		return nil
	}

	// If the error is a JSON-RPC error or a real HTTP error (not "endpoint not
	// found"), surface it directly — legacy SSE won't help.
	if !isEndpointNotFound(err) {
		return fmt.Errorf("initialize: %w", err)
	}

	// Fall back to legacy SSE transport.
	if sseErr := c.tryLegacySSE(ctx); sseErr != nil {
		return fmt.Errorf("streamable HTTP (%v); legacy SSE (%v)", err, sseErr)
	}

	// Now try initialize again via the discovered messages endpoint.
	if _, err2 := c.call(ctx, "initialize", params); err2 != nil {
		return fmt.Errorf("initialize (legacy SSE): %w", err2)
	}
	c.notify(ctx, "notifications/initialized", nil)
	return nil
}

// ListTools fetches the tools exposed by the server.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	result, err := c.call(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("decode tools: %w", err)
	}
	return resp.Tools, nil
}

// CallTool invokes a named tool and returns the concatenated text output.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	if len(arguments) == 0 {
		arguments = []byte("{}")
	}
	result, err := c.call(ctx, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", err
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(result, &resp) != nil {
		return string(result), nil
	}
	var sb strings.Builder
	for _, item := range resp.Content {
		if item.Type == "text" {
			sb.WriteString(item.Text)
		}
	}
	out := sb.String()
	if resp.IsError {
		return "ERROR: " + out, nil
	}
	return out, nil
}
