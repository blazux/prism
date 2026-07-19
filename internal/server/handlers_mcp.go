package server

import (
	"context"
	"encoding/json"
	"errors"
	"html"
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

	sessionID, sessOK := s.sessionFor(r, r.URL.Query().Get("session"))
	if !sessOK {
		http.Error(w, "forbidden", 403)
		return
	}
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
			// Route through sessionFor so a client cannot smuggle a foreign
			// namespace (another user's session or a group scope) in the body.
			mapped, ok := s.sessionFor(r, body.Session)
			if !ok {
				http.Error(w, "forbidden", 403)
				return
			}
			sessionID = mapped
		}
		tools, err := s.mcpMgr.Connect(r.Context(), sessionID, body.Name, body.URL, body.AuthSecret)
		if err != nil {
			// The server wants OAuth: run discovery + registration and hand the UI a
			// consent URL to open, instead of failing.
			var needAuth *mcp.OAuthRequiredError
			if errors.As(err, &needAuth) {
				redirectURI := externalBase(r) + "/api/oauth/mcp/callback"
				authURL, state, serr := s.mcpMgr.StartOAuth(r.Context(), sessionID, body.Name, body.URL, needAuth.ResourceMetadata, redirectURI)
				if serr != nil {
					http.Error(w, "oauth setup: "+serr.Error(), 500)
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"needs_oauth":   true,
					"authorize_url": authURL,
					"state":         state,
				})
				return
			}
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
	sessionID, sessOK := s.sessionFor(r, r.URL.Query().Get("session"))
	if !sessOK {
		http.Error(w, "forbidden", 403)
		return
	}
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

// handleMCPOAuthCallback finishes an MCP OAuth flow. The provider (Asana, Linear…)
// redirects the user's browser here with ?code&state after they consent. It is a
// cross-site top-level GET, so it is whitelisted from auth (see isOAuthCallback);
// the unguessable `state` is the CSRF guard. On success the little page tells the
// opener window to refresh its MCP list, then closes itself.
func (s *Server) handleMCPOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if e := r.URL.Query().Get("error"); e != "" {
		desc := r.URL.Query().Get("error_description")
		writeOAuthClosePage(w, "Authorization was refused: "+strings.TrimSpace(e+" "+desc))
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeOAuthClosePage(w, "Missing code or state in the callback.")
		return
	}
	if _, err := s.mcpMgr.CompleteOAuth(r.Context(), state, code); err != nil {
		writeOAuthClosePage(w, "Connection failed: "+err.Error())
		return
	}
	writeOAuthClosePage(w, "")
}

// writeOAuthClosePage renders the popup landing page. errMsg empty = success: it
// pings the opener (the MCP tab) to reload and closes; otherwise it shows the error.
func writeOAuthClosePage(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg == "" {
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Connected</title>
<body style="font:14px system-ui;padding:2rem;color:#2b7a3f">✓ Connected. You can close this window.
<script>try{window.opener&&window.opener.postMessage('mcp-oauth-done','*')}catch(e){}setTimeout(function(){window.close()},600)</script>`))
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Failed</title>
<body style="font:14px system-ui;padding:2rem;color:#b23">✗ ` + html.EscapeString(errMsg) + `<br><br>You can close this window and try again.`))
}
