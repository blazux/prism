package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPFailureReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"isError":true,"content":[{"type":"text","text":"fixture failure"}]}}`, req.ID)
	}))
	defer srv.Close()
	result, err := NewClient(srv.URL, "").CallTool(context.Background(), "fixture", json.RawMessage(`{}`))
	if err == nil || result != "" {
		t.Fatalf("behavior changed: result=%q err=%v", result, err)
	}

}
