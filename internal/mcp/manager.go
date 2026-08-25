package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

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
	// initGroup collapses concurrent getOrInitClient calls for the same key
	// into one dial+handshake — without it, two simultaneous tool calls for a
	// not-yet-cached server both pass the initial cache-miss check, both
	// Initialize(), and the second write silently wins in m.clients,
	// discarding the first (not a leak — Client holds no persistent
	// connection — just a wasted duplicate handshake, worth avoiding if a
	// server is stateful/rate-limited).
	initGroup singleflight.Group
	pendingMu sync.Mutex
	pending   map[string]*pendingOAuth // OAuth flows awaiting the consent redirect, keyed by state
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

	authHeader, err := m.bearerFor(ctx, sessionID, authSecret)
	if err != nil {
		return nil, err
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
			authHeader, err := m.bearerFor(ctx, sessionID, srv.AuthSecret)
			if err != nil {
				return "", fmt.Errorf("auth for MCP server %q: %w", srv.Name, err)
			}
			client, err := m.getOrInitClient(ctx, sessionID, srv.ID, srv.URL, authHeader)
			if err != nil {
				return "", fmt.Errorf("connect to MCP server %q: %w", srv.Name, err)
			}
			client.setAuth(authHeader) // a cached client may hold a now-refreshed token
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
	// singleflight.Do collapses concurrent callers for this key into a single
	// dial+handshake — see initGroup's doc comment. Note: if several callers
	// join the same in-flight call with different contexts, the work itself
	// runs under whichever caller's context started it; an early cancellation
	// there aborts it for every joined caller. Acceptable here (worst case:
	// one caller's cancel makes a sibling turn retry the init on its next
	// tool call) given the alternative is the duplicate-handshake race this
	// closes.
	v, err, _ := m.initGroup.Do(key, func() (interface{}, error) {
		m.clientsMu.Lock()
		if c, ok := m.clients[key]; ok {
			m.clientsMu.Unlock()
			return c, nil
		}
		m.clientsMu.Unlock()

		c := NewClient(url, authHeader)
		initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := c.Initialize(initCtx); err != nil {
			return nil, err
		}
		m.clientsMu.Lock()
		m.clients[key] = c
		m.clientsMu.Unlock()
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Client), nil
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
			params.Properties[propName] = schemaToProperty(raw)
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

// resolveSchemaType extracts a single JSON-Schema primitive type from a property
// schema. MCP servers (e.g. FastMCP for an Optional[...] parameter) emit the type
// as a plain string, an array like ["string","null"], or an anyOf/oneOf union.
// Ollama's minimal tool format carries only one type string, and strict backends
// (llama.cpp, used by Qwopus) reject an empty type with a 400 that kills the whole
// turn — so we pick the first non-"null" type and fall back to "string".
// schemaToProperty converts one JSON-Schema property node to the model's tool
// format — type, description, enum — and recurses into an array's `items` so the
// model knows the element type. Without it an array param arrives as a bare
// "array" and the model guesses whether elements are strings, numbers or objects.
func schemaToProperty(raw json.RawMessage) ollama.ToolProperty {
	var meta struct {
		Description string          `json:"description"`
		Items       json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(raw, &meta)
	p := ollama.ToolProperty{
		Type:        resolveSchemaType(raw),
		Description: meta.Description,
		Enum:        extractEnumValues(raw),
	}
	if len(meta.Items) > 0 {
		item := schemaToProperty(meta.Items)
		p.Items = &item
	}
	return p
}

func resolveSchemaType(raw json.RawMessage) string {
	var p struct {
		Type  json.RawMessage   `json:"type"`
		AnyOf []json.RawMessage `json:"anyOf"`
		OneOf []json.RawMessage `json:"oneOf"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return "string"
	}
	if t := firstSchemaType(p.Type); t != "" {
		return t
	}
	for _, u := range append(p.AnyOf, p.OneOf...) {
		var m struct {
			Type json.RawMessage `json:"type"`
		}
		if json.Unmarshal(u, &m) == nil {
			if t := firstSchemaType(m.Type); t != "" {
				return t
			}
		}
	}
	return "string"
}

// extractEnumValues pulls a property's "enum" allowed-values list out of its
// JSON-Schema so the model sees the constraint instead of guessing at a bare
// "string". Enums are usually strings; any non-string entries (numbers, bools)
// are stringified best-effort so nothing is silently dropped. Returns nil when
// there is no enum, so the field omitempty's away for unconstrained params.
func extractEnumValues(raw json.RawMessage) []string {
	var p struct {
		Enum []json.RawMessage `json:"enum"`
	}
	if json.Unmarshal(raw, &p) != nil || len(p.Enum) == 0 {
		return nil
	}
	out := make([]string, 0, len(p.Enum))
	for _, e := range p.Enum {
		var s string
		if json.Unmarshal(e, &s) == nil {
			out = append(out, s) // string enum entry, unquoted
		} else {
			out = append(out, strings.TrimSpace(string(e))) // number/bool/null: raw JSON literal
		}
	}
	return out
}

// firstSchemaType reads a JSON-Schema "type" that may be a string or an array of
// strings, returning the first non-"null" entry (or "" if none applies).
func firstSchemaType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s != "null" {
			return s
		}
		return ""
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		for _, t := range arr {
			if t != "" && t != "null" {
				return t
			}
		}
	}
	return ""
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
