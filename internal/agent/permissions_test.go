package agent

import "testing"

func TestToolPermission(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want Permission
	}{
		{"rag_search", nil, PermRAGRead},
		{"rag_list", nil, PermRAGRead},
		{"rag_manage", map[string]interface{}{"action": "list"}, PermRAGRead},
		{"rag_manage", map[string]interface{}{"action": "delete"}, PermRAGWrite},
		{"rag_ingest", nil, PermRAGWrite},
		{"rag_delete", nil, PermRAGWrite},
		{"docker_run", nil, PermTool},
		{"web_search", nil, PermTool},
		{"some_mcp_tool", nil, PermTool},
	}
	for _, c := range cases {
		if got := ToolPermission(c.name, c.args); got != c.want {
			t.Errorf("ToolPermission(%q, %v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}

// TestToolGuard_Enforcement checks the executor blocks unauthorized tools and
// allows authorized ones, simulating a Webex sender who may read the knowledge
// base but not run arbitrary tools.
func TestToolGuard_Enforcement(t *testing.T) {
	e := NewToolExecutor(nil, "", "", "", "")
	e.SetToolGuard(func(name string, args map[string]interface{}) error {
		if ToolPermission(name, args) == PermRAGRead {
			return nil
		}
		return errDenied
	})

	// A read-only RAG call passes the guard (then fails later for lack of a store,
	// which is fine — we only assert it wasn't blocked by the guard).
	if _, _, err := e.Execute(nil, "rag_search", []byte(`{"query":"x"}`)); err == errDenied {
		t.Fatal("rag_search should pass the guard for a RAG-read user")
	}
	// A tool call is blocked outright by the guard.
	if _, _, err := e.Execute(nil, "web_search", []byte(`{"query":"x"}`)); err != errDenied {
		t.Fatalf("web_search should be blocked by the guard, got err=%v", err)
	}
}

var errDenied = errTest("denied")

type errTest string

func (e errTest) Error() string { return string(e) }
