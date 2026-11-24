package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClientCatalogFollowsActualSource(t *testing.T) {
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "1")
	native := aiSource{ID: "paid-work", Provider: "anthropic", Model: "claude-opus-4-6"}
	compat := aiSource{ID: "anthropic", Provider: "openai", BaseURL: "http://127.0.0.1:1/v1", Model: "claude-sonnet-5"}
	b := &sourcesBackend{primary: native.ID, sources: []aiSource{native, compat}}
	for _, model := range []string{"", native.Model, "paid-work::claude-sonnet-4-6"} {
		if !b.UseClientToolCatalog(t.Context(), model) {
			t.Fatalf("native source lost catalog for %q", model)
		}
	}
	for _, model := range []string{"anthropic::claude-sonnet-5", "removed::claude-opus-4-6"} {
		if b.UseClientToolCatalog(t.Context(), model) {
			t.Fatalf("catalog enabled for incompatible source %q", model)
		}
	}
}

func TestDisabledClientCatalogDoesNotProbeLegacyModels(t *testing.T) {
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "")
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Write([]byte(`{"data":[{"id":"legacy-model"}]}`))
	}))
	defer srv.Close()
	b := &sourcesBackend{primary: "paid-work", sources: []aiSource{
		{ID: "paid-work", Provider: "anthropic", BaseURL: srv.URL, Model: "claude-opus-4-6"},
		{ID: "local", Provider: "openai", BaseURL: srv.URL, Model: "default"},
	}}
	if b.UseClientToolCatalog(t.Context(), "legacy-model") || requests.Load() != 0 {
		t.Fatalf("disabled optimization triggered model discovery: %d requests", requests.Load())
	}
}
