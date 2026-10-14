package agent

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

func TestMCPRefreshAgentActionAndGroupPermissions(t *testing.T) {
	ms := resourceDatabase(t)
	ctx := t.Context()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		result := `{"protocolVersion":"2024-11-05","capabilities":{}}`
		if req.Method == "tools/list" {
			result = `{"tools":[{"name":"fresh_tool","inputSchema":{"type":"object","properties":{}}}]}`
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, result)
	}))
	defer remote.Close()
	for _, multi := range []bool{false, true} {
		e := &ToolExecutor{multiUser: multi, sessionID: "u7-board", ragScope: "g5", sharingGroups: []memory.Membership{{GroupID: 5, Role: "admin"}}}
		reloaded := false
		e.SetMCPManager(mcp.NewManager(ms), func() { reloaded = true })
		scope, _ := e.mcpWriteScope()
		if err := ms.MCPUpsertServer(ctx, scope, "fixture", "Fixture", remote.URL, "", true, []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
		if multi {
			e.sharingGroups[0].Role = "member"
			out, _, err := e.execute(ctx, "mcp", []byte(`{"action":"refresh","name":"fixture"}`))
			if err != nil || !strings.Contains(out, "group admin") || reloaded {
				t.Fatal("member could refresh group server", out, err)
			}
			e.sharingGroups[0].Role = "admin"
		}
		out, _, err := e.execute(ctx, "mcp", []byte(`{"action":"refresh","name":"fixture"}`))
		if err != nil || !strings.Contains(out, "1 tools") || !reloaded {
			t.Fatal(out, err)
		}
		tools := e.AllDynamicTools()
		if len(tools) != 1 || tools[0].Function.Name != "fresh_tool" {
			t.Fatal("agent did not see refreshed tools", tools)
		}
	}
}
