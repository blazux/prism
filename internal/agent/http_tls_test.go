package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateTLSIsEvaluatedAfterRedirect(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("fixture")) }))
	defer tlsServer.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, tlsServer.URL, 302) }))
	defer redirect.Close()
	e := &ToolExecutor{}
	out, err := e.httpRequest(context.Background(), "GET", redirect.URL, nil, "")
	if err != nil || !strings.Contains(out, "fixture") {
		t.Fatal(out, err)
	}
}
