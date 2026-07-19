package mcp

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Live end-to-end of discovery + DCR + authorize-URL against the real Asana MCP
// server. Skipped unless MCP_LIVE=1, because it hits the network.
func TestLiveAsanaDiscoveryAndRegister(t *testing.T) {
	if strings.TrimSpace(os.Getenv("MCP_LIVE")) == "" {
		t.Skip("set MCP_LIVE=1 to run the live Asana test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	hc := &http.Client{Timeout: 15 * time.Second}

	// This is the exact resource_metadata Asana points at in its 401.
	meta, err := discoverOAuth(ctx, hc,
		"https://mcp.asana.com/.well-known/oauth-protected-resource",
		"https://mcp.asana.com/sse")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	t.Logf("resource=%s authorize=%s token=%s register=%s",
		meta.Resource, meta.AuthorizationEndpoint, meta.TokenEndpoint, meta.RegistrationEndpoint)

	if err := meta.registerClient(ctx, hc, "http://localhost:48080/api/oauth/mcp/callback"); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Logf("registered client_id=%s", meta.ClientID)

	verifier := "test-verifier-1234567890-abcdefghijklmnopqrstuvwxyz-ABCDEF"
	u := meta.authorizeURL("http://localhost:48080/api/oauth/mcp/callback", "state123", verifier)
	t.Logf("authorize URL: %s", u)
	for _, must := range []string{"code_challenge=", "code_challenge_method=S256", "resource=", "client_id=" + meta.ClientID, "state=state123"} {
		if !strings.Contains(u, must) {
			t.Errorf("authorize URL missing %q", must)
		}
	}
}
