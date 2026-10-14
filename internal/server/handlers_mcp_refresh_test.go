package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/mcp"
	"prism/internal/memory"
)

func TestMCPRefreshRoutesRespectPersonalAndGroupScopes(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		result := `{"protocolVersion":"2024-11-05","capabilities":{}}`
		if req.Method == "tools/list" {
			result = `{"tools":[{"name":"new_tool","description":"updated","inputSchema":{"type":"object","properties":{}}}]}`
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, result)
	}))
	defer upstream.Close()
	s := &Server{memStore: ms, mcpMgr: mcp.NewManager(ms)}
	seed := func(scope string) {
		t.Helper()
		if err := ms.MCPUpsertServer(ctx, scope, "fixture", "Fixture", upstream.URL, "", true, []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
	}
	seed("u11")
	u := &memory.User{ID: 11}
	request := func(path string, user *memory.User, group bool, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest("POST", path, nil), user)
		if group {
			s.handleGroupMCP(w, r)
		} else {
			s.handleMCPServerByID(w, r)
		}
		if w.Code != want {
			t.Fatalf("%s: status %d, want %d: %s", path, w.Code, want, w.Body.String())
		}
		if want == 200 && !strings.Contains(w.Body.String(), `"new_tool"`) {
			t.Fatal("new catalogue missing from response", w.Body.String())
		}
	}
	request("/api/mcp/servers/fixture?session=board", u, false, 200)
	request("/api/mcp/servers/fixture?session=board", &memory.User{ID: 12}, false, 404)
	request("/api/mcp/servers/fixture?session=u12-board", u, false, 403)
	s.cfg.MultiUser = true
	request("/api/mcp/servers/fixture?session=board", u, false, 403)
	admin, err := ms.CreateUser(ctx, "admin@example.test", "unused", "Admin", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	member, err := ms.CreateUser(ctx, "member@example.test", "unused", "Member", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	g, err := ms.CreateGroup(ctx, "Refresh fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, g.ID, admin.ID, memory.GroupRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, g.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	seed(fmt.Sprintf("g%d", g.ID))
	if err := ms.SetGroupToolAccess(ctx, g.ID, "new_tool", "admin_only"); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/group/mcp?group=%d&id=fixture", g.ID)
	request(path, member, true, 403)
	request(fmt.Sprintf("/api/group/mcp?group=%d&id=fixture", g.ID+1), admin, true, 403)
	request(path, admin, true, 200)
	policies, err := ms.GroupToolPolicies(ctx, g.ID)
	if err != nil || policies["new_tool"] != "admin_only" {
		t.Fatal("refresh changed group restrictions", policies, err)
	}
}
