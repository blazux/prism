package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
	"prism/internal/memory"
)

func refreshStore(t *testing.T) *memory.Store {
	t.Helper()
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("mcp_refresh_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); db.Close(ctx) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	ms, err := memory.NewStore(ctx, u.String(), bytes.Repeat([]byte{7}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ms.Close)
	return ms
}

func TestRefreshReplacesCatalogueAndConnectionPreservingSettings(t *testing.T) {
	ms := refreshStore(t)
	ctx := t.Context()
	for _, auth := range []string{"api_token", "mcp_oauth_fixture"} {
		t.Run(auth, func(t *testing.T) {
			// Both bearer tokens and existing OAuth credentials must be reused,
			// without restarting consent or asking the user for the secret again.
			secret := "fixture-token"
			if auth == "mcp_oauth_fixture" {
				b, _ := json.Marshal(oauthState{Token: &oauth2.Token{AccessToken: secret, TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}})
				secret = string(b)
			}
			if err := ms.ConfigScope(auth).SetSecret(ctx, auth, secret); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			version, fail := 1, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("stored authentication not reused")
					w.WriteHeader(401)
					return
				}
				var req struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				mu.Lock()
				v, broken := version, fail
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if req.Method == "notifications/initialized" {
					w.WriteHeader(202)
					return
				}
				if req.Method == "initialize" {
					w.Header().Set("Mcp-Session-Id", fmt.Sprintf("session-%d", v))
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}`, req.ID)
					return
				}
				if req.Method == "tools/list" {
					if broken {
						fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"fixture discovery failed"}}`, req.ID)
						return
					}
					names := []string{"kept", "removed"}
					if v == 2 {
						names = []string{"kept", "added"}
					}
					var tools []Tool
					schema := json.RawMessage(`{"type":"object","properties":{}}`)
					if v == 2 {
						schema = json.RawMessage(`{"type":"object","properties":{"new_argument":{"type":"string"}}}`)
					}
					for _, name := range names {
						tools = append(tools, Tool{Name: name, Description: fmt.Sprintf("version %d", v), InputSchema: schema})
					}
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": tools}})
					return
				}
				if req.Method == "tools/call" {
					if r.Header.Get("Mcp-Session-Id") != "session-2" {
						t.Error("refresh did not replace the stale connection")
					}
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"new connection"}]}}`, req.ID)
				}
			}))
			defer srv.Close()
			m := NewManager(ms)
			if _, err := m.Connect(ctx, auth, "Fixture MCP", srv.URL, auth); err != nil {
				t.Fatal(err)
			}
			if err := m.SetEnabled(ctx, auth, "fixture-mcp", false); err != nil {
				t.Fatal(err)
			}
			before, _ := ms.MCPListServers(ctx, auth)
			oldClient := m.clients[auth+":fixture-mcp"]
			mu.Lock()
			version = 2
			mu.Unlock()
			tools, err := m.Refresh(ctx, auth, "fixture-mcp")
			if err != nil {
				t.Fatal(err)
			}
			if len(tools) != 2 || tools[0].Description != "version 2" || tools[1].Name != "added" {
				t.Fatal(tools)
			}
			after, _ := ms.MCPListServers(ctx, auth)
			oldJSON, newJSON := before[0].ToolsJSON, after[0].ToolsJSON
			after[0].ToolsJSON = oldJSON
			if !reflect.DeepEqual(before, after) {
				t.Fatal("refresh changed configuration or enabled state", before, after)
			}
			if m.clients[auth+":fixture-mcp"] == oldClient {
				t.Fatal("connection not renewed")
			}
			if got := m.ToOllamaTools(auth); len(got) != 0 {
				t.Fatal("disabled server exposed tools")
			}
			if err := m.SetEnabled(ctx, auth, "fixture-mcp", true); err != nil {
				t.Fatal(err)
			}
			if m.HasTool(auth, "removed") || !m.HasTool(auth, "added") {
				t.Fatal("agent catalogue not refreshed")
			}
			visible := m.ToOllamaTools(auth)
			if len(visible) != 2 || visible[0].Function.Parameters.Properties["new_argument"].Type != "string" {
				t.Fatal("changed tool schema not exposed to the agent", visible)
			}
			if result, err := m.CallTool(ctx, auth, "added", nil); err != nil || result != "new connection" {
				t.Fatal(result, err)
			}
			refreshedClient := m.clients[auth+":fixture-mcp"]
			mu.Lock()
			fail = true
			mu.Unlock()
			if _, err := m.Refresh(ctx, auth, "fixture-mcp"); err == nil {
				t.Fatal("failed discovery accepted")
			}
			after, _ = ms.MCPListServers(ctx, auth)
			if !bytes.Equal(after[0].ToolsJSON, newJSON) || m.clients[auth+":fixture-mcp"] != refreshedClient {
				t.Fatal("failed refresh replaced working catalogue/connection")
			}
			if _, err := m.Refresh(ctx, "another-user", "fixture-mcp"); !errors.Is(err, ErrServerNotFound) {
				t.Fatal("cross-scope refresh", err)
			}
		})
	}
}

func TestRefreshDoesNotRestoreDeletedOrReplacedServer(t *testing.T) {
	ms := refreshStore(t)
	ctx := t.Context()
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			started, release := make(chan struct{}, 1), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				if req.Method == "initialize" {
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}`, req.ID)
				} else if req.Method == "tools/list" {
					started <- struct{}{}
					<-release
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}`, req.ID)
				} else {
					w.WriteHeader(202)
				}
			}))
			defer srv.Close()
			m := NewManager(ms)
			if err := ms.MCPUpsertServer(ctx, "scope", "fixture", "Fixture", srv.URL, "", true, []byte(`[]`)); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := m.Refresh(ctx, "scope", "fixture"); done <- err }()
			<-started
			if replace {
				if err := ms.MCPUpsertServer(ctx, "scope", "fixture", "Replacement", srv.URL, "", false, []byte(`[]`)); err != nil {
					t.Fatal(err)
				}
			} else if err := m.RemoveByID(ctx, "scope", "fixture"); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-done; !errors.Is(err, ErrServerChanged) {
				t.Fatal("stale discovery accepted", err)
			}
			if m.clients["scope:fixture"] != nil {
				t.Fatal("stale client cached")
			}
			rows, err := ms.MCPListServers(ctx, "scope")
			if err != nil {
				t.Fatal(err)
			}
			if !replace && len(rows) != 0 {
				t.Fatal("deleted server resurrected")
			}
			if replace && (len(rows) != 1 || rows[0].Enabled || rows[0].Name != "Replacement") {
				t.Fatal("replacement overwritten", rows)
			}
		})
	}
}
