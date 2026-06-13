package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"prism/internal/mcp"
)

// handleMCPServers handles GET /api/mcp/servers?session=<id>
// and POST /api/mcp/servers (body: {session, name, url, auth_secret?}).
func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		return
	}

	sessionID := sanitizeSessionID(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = "default"
	}

	switch r.Method {
	case "GET":
		servers, err := s.mcpMgr.List(r.Context(), sessionID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if servers == nil {
			servers = []mcp.ServerConfig{}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"servers": servers})

	case "POST":
		var body struct {
			Session    string `json:"session"`
			Name       string `json:"name"`
			URL        string `json:"url"`
			AuthSecret string `json:"authSecret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.URL == "" {
			http.Error(w, "name and url required", 400)
			return
		}
		if body.Session != "" {
			sessionID = sanitizeSessionID(body.Session)
		}
		tools, err := s.mcpMgr.Connect(r.Context(), sessionID, body.Name, body.URL, body.AuthSecret)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.broadcastMCP(sessionID)
		json.NewEncoder(w).Encode(map[string]interface{}{"tools": tools, "count": len(tools)})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleMCPServerByID handles DELETE and PATCH /api/mcp/servers/<id>?session=<sid>.
func (s *Server) handleMCPServerByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/mcp/servers/")
	if id == "" {
		http.Error(w, "missing server id", 400)
		return
	}
	sessionID := sanitizeSessionID(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = "default"
	}

	switch r.Method {
	case "DELETE":
		if err := s.mcpMgr.RemoveByID(r.Context(), sessionID, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.broadcastMCP(sessionID)
		w.WriteHeader(204)

	case "PATCH":
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
			http.Error(w, "enabled field required", 400)
			return
		}
		if err := s.mcpMgr.SetEnabled(r.Context(), sessionID, id, *body.Enabled); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.broadcastMCP(sessionID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// broadcastMCP pushes updated MCP server list to all WS clients for a session.
func (s *Server) broadcastMCP(sessionID string) {
	servers, _ := s.mcpMgr.List(context.Background(), sessionID)
	if servers == nil {
		servers = []mcp.ServerConfig{}
	}
	msg, _ := json.Marshal(map[string]interface{}{
		"type":    "mcp_updated",
		"session": sessionID,
		"servers": servers,
	})
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if c.sessionID == sessionID {
			select {
			case c.send <- msg:
			default:
			}
		}
	}
}
