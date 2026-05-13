package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"prism/internal/memory"
	"prism/internal/ollama"
)

// ServerConfig is the full view of a configured MCP server including its cached tools.
type ServerConfig struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	AuthSecret string `json:"authSecret"`
	Enabled    bool   `json:"enabled"`
	Tools      []Tool `json:"tools"`
}

// Manager manages per-session MCP server configurations backed by the DB.
type Manager struct {
	mu        sync.RWMutex
	store     *memory.Store
	clientsMu sync.Mutex
	clients   map[string]*Client // keyed by "sessionID:serverID", populated lazily
}

func NewManager(ms *memory.Store) *Manager {
	return &Manager{store: ms, clients: make(map[string]*Client)}
}

// SetStore updates the backing store (called when Postgres becomes available).
func (m *Manager) SetStore(ms *memory.Store) {
	m.mu.Lock()
	m.store = ms
	m.mu.Unlock()
}

func (m *Manager) getStore() *memory.Store {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store
}

// Connect tests the server, fetches its tools, and persists the config.
func (m *Manager) Connect(ctx context.Context, sessionID, name, url, authSecret string) ([]Tool, error) {
	ms := m.getStore()
	if ms == nil {
		return nil, fmt.Errorf("database not available (Postgres required)")
	}

	authHeader := ""
	if authSecret != "" {
		val, exists, err := ms.GetSecret(ctx, authSecret)
		if err != nil {
			return nil, fmt.Errorf("get secret %q: %w", authSecret, err)
		}
		if !exists {
			return nil, fmt.Errorf("secret %q not found — call request_secret first", authSecret)
		}
		authHeader = "Bearer " + val
	}

	connCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	client := NewClient(url, authHeader)
	if err := client.Initialize(connCtx); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	tools, err := client.ListTools(connCtx)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	toolsJSON, _ := json.Marshal(tools)
	id := slugify(name)
	if err := ms.MCPUpsertServer(ctx, sessionID, id, name, url, authSecret, true, toolsJSON); err != nil {
		return nil, fmt.Errorf("save server: %w", err)
	}

	m.clientsMu.Lock()
	m.clients[sessionID+":"+id] = client
	m.clientsMu.Unlock()

	return tools, nil
}

// RemoveByID deletes a server by its slug ID.
func (m *Manager) RemoveByID(ctx context.Context, sessionID, id string) error {
	ms := m.getStore()
	if ms == nil {
		return fmt.Errorf("database not available")
	}
	m.clientsMu.Lock()
	delete(m.clients, sessionID+":"+id)
	m.clientsMu.Unlock()
	return ms.MCPDeleteServer(ctx, sessionID, id)
}

// Remove deletes a server by its human name (converts to slug first).
func (m *Manager) Remove(ctx context.Context, sessionID, name string) error {
	return m.RemoveByID(ctx, sessionID, slugify(name))
}

// SetEnabled enables or disables a server by slug ID.
func (m *Manager) SetEnabled(ctx context.Context, sessionID, id string, enabled bool) error {
	ms := m.getStore()
	if ms == nil {
		return fmt.Errorf("database not available")
	}
	return ms.MCPSetEnabled(ctx, sessionID, id, enabled)
}

// List returns all configured servers for a session with their cached tools.
func (m *Manager) List(ctx context.Context, sessionID string) ([]ServerConfig, error) {
	ms := m.getStore()
	if ms == nil {
		return nil, nil
	}
	rows, err := ms.MCPListServers(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	servers := make([]ServerConfig, 0, len(rows))
	for _, row := range rows {
		srv := ServerConfig{
			ID:         row.ID,
			SessionID:  row.SessionID,
			Name:       row.Name,
			URL:        row.URL,
			AuthSecret: row.AuthSecret,
			Enabled:    row.Enabled,
		}
		if len(row.ToolsJSON) > 0 {
			_ = json.Unmarshal(row.ToolsJSON, &srv.Tools)
		}
		servers = append(servers, srv)
	}
	return servers, nil
}

// ToOllamaTools returns all tools from enabled servers in Ollama format.
// Uses a short background context; safe to call from callOllama.
func (m *Manager) ToOllamaTools(sessionID string) []ollama.Tool {
	ms := m.getStore()
	if ms == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	servers, err := m.List(ctx, sessionID)
	if err != nil || len(servers) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var tools []ollama.Tool
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		for _, t := range srv.Tools {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			tools = append(tools, toOllamaTool(t))
		}
	}
	return tools
}

// HasTool returns true if the given tool name is provided by any enabled MCP server.
func (m *Manager) HasTool(sessionID, toolName string) bool {
	ms := m.getStore()
	if ms == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	servers, err := m.List(ctx, sessionID)
	if err != nil {
		return false
	}
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		for _, t := range srv.Tools {
			if t.Name == toolName {
				return true
			}
		}
	}
	return false
}

// CallTool routes a tool call to the correct enabled MCP server.
func (m *Manager) CallTool(ctx context.Context, sessionID, toolName string, args json.RawMessage) (string, error) {
	ms := m.getStore()
	if ms == nil {
		return "", fmt.Errorf("database not available")
	}
	servers, err := m.List(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		for _, t := range srv.Tools {
			if t.Name != toolName {
				continue
			}
			authHeader := ""
			if srv.AuthSecret != "" {
				if val, exists, _ := ms.GetSecret(ctx, srv.AuthSecret); exists {
					authHeader = "Bearer " + val
				}
			}
			client, err := m.getOrInitClient(ctx, sessionID, srv.ID, srv.URL, authHeader)
			if err != nil {
				return "", fmt.Errorf("connect to MCP server %q: %w", srv.Name, err)
			}
			return client.CallTool(ctx, toolName, args)
		}
	}
	return "", fmt.Errorf("unknown MCP tool: %s", toolName)
}

// getOrInitClient returns a cached initialized client, creating one if needed.
func (m *Manager) getOrInitClient(ctx context.Context, sessionID, serverID, url, authHeader string) (*Client, error) {
	key := sessionID + ":" + serverID
	m.clientsMu.Lock()
	client, ok := m.clients[key]
	m.clientsMu.Unlock()
	if ok {
		return client, nil
	}
	client = NewClient(url, authHeader)
	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := client.Initialize(initCtx); err != nil {
		return nil, err
	}
	m.clientsMu.Lock()
	m.clients[key] = client
	m.clientsMu.Unlock()
	return client, nil
}

// toOllamaTool converts an MCP tool definition to Ollama's format.
// MCP inputSchema is a JSON Schema subset; we map the fields Ollama supports.
func toOllamaTool(t Tool) ollama.Tool {
	params := ollama.ToolParameters{
		Type:       "object",
		Properties: map[string]ollama.ToolProperty{},
	}
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if json.Unmarshal(t.InputSchema, &schema) == nil {
		if schema.Type != "" {
			params.Type = schema.Type
		}
		params.Required = schema.Required
		for propName, raw := range schema.Properties {
			var prop struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			}
			_ = json.Unmarshal(raw, &prop)
			params.Properties[propName] = ollama.ToolProperty{
				Type:        prop.Type,
				Description: prop.Description,
			}
		}
	}
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		},
	}
}

func slugify(s string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prevDash = false
		} else if !prevDash && sb.Len() > 0 {
			sb.WriteRune('-')
			prevDash = true
		}
	}
	return strings.TrimRight(sb.String(), "-")
}
