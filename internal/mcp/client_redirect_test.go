package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// RTClient mounts its MCP transport at /mcp/, and Starlette answers 307 on /mcp.
// Our client strips the trailing slash from the configured URL, so every POST
// hits /mcp and must survive the redirect — Go replays a 307 POST only because
// the body is a bytes.Reader (GetBody is set). This pins that.
func TestClientFollowsTrailingSlashRedirectOnPost(t *testing.T) {
	var gotBodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			http.Redirect(w, r, "/mcp/", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path != "/mcp/" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBodies = append(gotBodies, string(b))
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header lost across the redirect: %q", r.Header.Get("Authorization"))
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Mcp-Session-Id", "sess-1")
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}`, req.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"list_tickets","description":"d","inputSchema":{}}]}}`, req.ID)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{}}`, req.ID)
		}
	}))
	defer srv.Close()

	// Configured with the trailing slash, exactly as the README tells the admin.
	c := NewClient(srv.URL+"/mcp/", "Bearer tok")
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize through the 307: %v", err)
	}
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "list_tickets" {
		t.Fatalf("tools not discovered: %+v", tools)
	}
	// The body must have survived the replay, not arrived empty.
	for i, b := range gotBodies {
		if !strings.Contains(b, `"jsonrpc"`) {
			t.Errorf("request %d reached the server with an empty body: %q", i, b)
		}
	}
}
