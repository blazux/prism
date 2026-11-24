package server

import "testing"

func TestOpenAIClientCatalogFollowsResolvedSourceAndRequestedModel(t *testing.T) {
	t.Setenv("PRISM_OPENAI_LAZY_TOOLS", "1")
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "")
	paid := aiSource{ID: "paid-work", Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-5.5"}
	local := aiSource{ID: "openai", Provider: "openai", BaseURL: "http://127.0.0.1:1/v1", Model: "gpt-5.5"}
	b := &sourcesBackend{primary: paid.ID, sources: []aiSource{paid, local}}
	for _, model := range []string{"", paid.Model, "paid-work::gpt-4.1-mini", "paid-work::gpt-5.5"} {
		if !b.UseClientToolCatalog(t.Context(), model) {
			t.Fatalf("official source lost catalog for %q", model)
		}
	}
	for _, model := range []string{"openai::gpt-5.5", "paid-work::gpt-5.5-pro", "removed::gpt-5.5"} {
		if b.UseClientToolCatalog(t.Context(), model) {
			t.Fatalf("catalog enabled for incompatible source/model %q", model)
		}
	}
	// Selecting a supported chat model must work even if the same connection's
	// default is a model that does not support this catalog experiment.
	b.sources[0].Model = "gpt-4o-realtime-preview"
	if !b.UseClientToolCatalog(t.Context(), "paid-work::gpt-5.5") {
		t.Fatal("the connection's default model overrode the selected model's capability")
	}
	if b.UseClientToolCatalog(t.Context(), "") {
		t.Fatal("an unsupported default model acquired the catalog")
	}
	b.primary = local.ID
	if b.UseClientToolCatalog(t.Context(), "") {
		t.Fatal("the local default inherited another source's catalog capability")
	}
}

func TestDisabledOpenAIClientCatalogKeepsQualifiedSourcesOrdinary(t *testing.T) {
	t.Setenv("PRISM_OPENAI_LAZY_TOOLS", "")
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "")
	b := &sourcesBackend{primary: "paid-work", sources: []aiSource{
		{ID: "paid-work", Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-5.5"},
	}}
	for _, model := range []string{"", "gpt-5.5", "paid-work::gpt-5.5"} {
		if b.UseClientToolCatalog(t.Context(), model) {
			t.Fatalf("default-off optimization enabled for %q", model)
		}
	}
}
