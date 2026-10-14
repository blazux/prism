package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListToolsCompleteDiscoveryOrError(t *testing.T) {
	for _, mode := range []string{"pages", "later-error", "repeated-cursor", "invalid", "empty"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int `json:"id"`
					Params struct {
						Cursor string `json:"cursor"`
					} `json:"params"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				result := `{"tools":[{"name":"first"}],"nextCursor":"page2"}`
				switch {
				case mode == "invalid":
					result = `{}`
				case mode == "empty":
					result = `{"tools":[]}`
				case req.Params.Cursor == "page2" && mode == "later-error":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32603,"message":"page unavailable"}}`, req.ID)
					return
				case req.Params.Cursor == "page2" && mode == "pages":
					result = `{"tools":[{"name":"second"}]}`
				}
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			}))
			defer srv.Close()
			tools, err := NewClient(srv.URL, "").ListTools(context.Background())
			if mode == "pages" {
				if err != nil || len(tools) != 2 || tools[1].Name != "second" {
					t.Fatal(tools, err)
				}
			} else if mode == "empty" {
				if err != nil || len(tools) != 0 {
					t.Fatal(tools, err)
				}
			} else if err == nil || tools != nil {
				t.Fatal("partial or invalid catalogue accepted", tools, err)
			}
		})
	}
}
