package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/agent"
	"prism/internal/mcp"
	"prism/internal/memory"
)

func TestSingleOwnerMCPMigratesWithoutLosingConnectionsOrCredentials(t *testing.T) {
	ms, dsn := securityStoreWithDSN(t)
	ctx := t.Context()
	for _, item := range []struct {
		scope, id, name, secret string
		enabled                 bool
	}{
		{"scan", "same", "Legacy", "TOKEN", false},
		{"u7", "oauth", "OAuth", "mcp_oauth_oauth", true},
		{"global", "same", "Existing", "TOKEN", true},
		{"g9", "group", "Group", "", true},
	} {
		tools, _ := json.Marshal([]map[string]any{{"name": "fixture_tool", "description": item.name, "inputSchema": map[string]any{"type": "object"}}})
		if err := ms.MCPUpsertServer(ctx, item.scope, item.id, item.name, "https://fixture.invalid/mcp", item.secret, item.enabled, tools); err != nil {
			t.Fatal(err)
		}
	}
	ms.SetSecret(ctx, "TOKEN", "global token")
	ms.ConfigScope("scan").SetSecret(ctx, "TOKEN", "legacy token")
	ms.ConfigScope("u7").SetSecret(ctx, "mcp_oauth_oauth", `{"fixture":"oauth state"}`)
	// Multi-user mode must not promote any personal or board registry.
	mu, err := memory.NewStore(ctx, dsn, bytes.Repeat([]byte{5}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	mu.Close()
	if rows, _ := ms.MCPListServers(ctx, "scan"); len(rows) != 1 {
		t.Fatal("multi-user migrated personal connections")
	}
	owner, err := memory.NewStore(ctx, dsn, bytes.Repeat([]byte{5}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	rows, err := owner.MCPListServers(ctx, "global")
	if err != nil || len(rows) != 3 {
		t.Fatal("connections lost", len(rows), err)
	}
	for _, row := range rows {
		value, ok, err := owner.GetSecret(ctx, row.AuthSecret)
		if !ok || err != nil {
			t.Fatal("credential lost", row.Name, err)
		}
		switch row.Name {
		case "Existing":
			if row.ID != "same" || value != "global token" {
				t.Fatal("existing connection overwritten")
			}
		case "Legacy":
			if row.ID == "same" || row.Enabled || value != "legacy token" {
				t.Fatal("legacy connection changed")
			}
		case "OAuth":
			if !strings.HasPrefix(row.AuthSecret, "mcp_oauth_") || value != `{"fixture":"oauth state"}` {
				t.Fatal("OAuth state lost")
			}
		}
	}
	if group, _ := owner.MCPListServers(ctx, "g9"); len(group) != 1 {
		t.Fatal("group registry changed")
	}
	again, err := memory.NewStore(ctx, dsn, bytes.Repeat([]byte{5}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if rows, _ := owner.MCPListServers(ctx, "global"); len(rows) != 3 {
		t.Fatal("migration is not idempotent")
	}
	s := &Server{memStore: owner, mcpMgr: mcp.NewManager(owner)}
	tools := s.mcpMgr.ToOllamaTools("global")
	if len(tools) != 1 || tools[0].Function.Description != "Existing" {
		t.Fatal("legacy tool masked the existing installation connection", tools)
	}
	var first string
	for _, path := range []string{"/api/mcp/servers", "/api/mcp/servers?session=scan", "/api/mcp/servers?session=another"} {
		w := httptest.NewRecorder()
		s.handleMCPServers(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if first == "" {
			first = w.Body.String()
		} else if first != w.Body.String() {
			t.Fatal("MCP settings depend on workspace")
		}
	}
	for _, session := range []string{"default", "scan", "u7-other"} {
		e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
		e.SetSessionID(session)
		e.SetMCPManager(s.mcpMgr, nil)
		servers, err := e.MCPServers(ctx)
		if err != nil || len(servers) != 3 {
			t.Fatal("agent registry differs from Settings", session, err)
		}
		out, _, err := e.Execute(ctx, "mcp", []byte(`{"action":"list"}`))
		if err != nil || !strings.Contains(out, "Legacy") || !strings.Contains(out, "Existing") {
			t.Fatal("agent cannot list connections", out, err)
		}
	}
}

func TestMCPBroadcastUpdatesEverySingleOwnerChat(t *testing.T) {
	ms := securityStore(t)
	if err := ms.MCPUpsertServer(t.Context(), "global", "fixture", "Fixture", "https://fixture.invalid", "", true, []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	a := &Client{sessionID: "first", send: make(chan []byte, 1)}
	b := &Client{sessionID: "second", send: make(chan []byte, 1)}
	s := &Server{memStore: ms, mcpMgr: mcp.NewManager(ms), clients: map[*Client]struct{}{a: {}, b: {}}}
	s.broadcastMCP("first")
	for _, c := range []*Client{a, b} {
		select {
		case raw := <-c.send:
			var result struct {
				Session string
				Servers []mcp.ServerConfig
			}
			json.Unmarshal(raw, &result)
			if result.Session != c.sessionID || len(result.Servers) != 1 {
				t.Fatal("incorrect update", string(raw))
			}
		default:
			t.Fatal("another workspace did not receive the registry update")
		}
	}
}
