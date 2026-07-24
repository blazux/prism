package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrent callers asking for the same not-yet-cached client must collapse
// into a single dial+handshake — before the singleflight fix, every one of
// them would pass the cache-miss check and independently Initialize(), with
// only the last write surviving in m.clients (the others' connections just
// discarded). This pins the fix: N concurrent calls, exactly one real
// "initialize" JSON-RPC request should reach the server.
func TestGetOrInitClient_ConcurrentCallsDedupe(t *testing.T) {
	var initCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "initialize" {
			atomic.AddInt32(&initCalls, 1)
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}`, req.ID)
	}))
	defer srv.Close()

	m := NewManager(nil)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	clients := make([]*Client, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := m.getOrInitClient(context.Background(), "sess-1", "srv-1", srv.URL, "")
			clients[i], errs[i] = c, err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: unexpected error: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if clients[i] != clients[0] {
			t.Errorf("caller %d got a different *Client than caller 0 — should share one cached instance", i)
		}
	}
	if got := atomic.LoadInt32(&initCalls); got != 1 {
		t.Errorf("initialize hit the server %d times for %d concurrent callers, want exactly 1", got, n)
	}
}
