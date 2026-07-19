package mcp

import "testing"

func TestResourceMetadataFromChallenge(t *testing.T) {
	// The exact header Asana returns.
	h := `Bearer realm="OAuth", resource_metadata="https://mcp.asana.com/.well-known/oauth-protected-resource", error="invalid_token"`
	got, ok := resourceMetadataFromChallenge(h)
	if !ok || got != "https://mcp.asana.com/.well-known/oauth-protected-resource" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := resourceMetadataFromChallenge(`Bearer realm="OAuth"`); ok {
		t.Error("a challenge without resource_metadata must return ok=false")
	}
}

func TestOriginOf(t *testing.T) {
	if got := originOf("https://mcp.asana.com/sse"); got != "https://mcp.asana.com" {
		t.Errorf("originOf = %q", got)
	}
}
