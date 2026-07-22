package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"prism/internal/mcp"
)

// Personal MCP servers must be refused outright (403) in MULTI_USER mode —
// group MCP (Admin console) is the only path — and remain fully reachable in
// single-user mode, the exact regression this change must not introduce.
func TestHandleMCPServers_MultiUserRefused(t *testing.T) {
	multi := &Server{cfg: Config{MultiUser: true}, mcpMgr: mcp.NewManager(nil)}
	w := httptest.NewRecorder()
	multi.handleMCPServers(w, httptest.NewRequest("GET", "/api/mcp/servers", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("multi-user GET /api/mcp/servers: status=%d, want %d", w.Code, http.StatusForbidden)
	}

	single := &Server{cfg: Config{MultiUser: false}, mcpMgr: mcp.NewManager(nil)}
	w = httptest.NewRecorder()
	single.handleMCPServers(w, httptest.NewRequest("GET", "/api/mcp/servers", nil))
	if w.Code == http.StatusForbidden {
		t.Error("single-user mode: personal MCP servers must remain reachable, got 403")
	}
}

func TestHandleMCPServerByID_MultiUserRefused(t *testing.T) {
	multi := &Server{cfg: Config{MultiUser: true}, mcpMgr: mcp.NewManager(nil)}
	w := httptest.NewRecorder()
	multi.handleMCPServerByID(w, httptest.NewRequest("DELETE", "/api/mcp/servers/some-id", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("multi-user DELETE /api/mcp/servers/<id>: status=%d, want %d", w.Code, http.StatusForbidden)
	}

	single := &Server{cfg: Config{MultiUser: false}, mcpMgr: mcp.NewManager(nil)}
	w = httptest.NewRecorder()
	single.handleMCPServerByID(w, httptest.NewRequest("DELETE", "/api/mcp/servers/some-id", nil))
	if w.Code == http.StatusForbidden {
		t.Error("single-user mode: personal MCP server deletion must remain reachable, got 403")
	}
}
