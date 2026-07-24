package mcp

// The OAuth authorization flow, on top of the discovery/DCR/PKCE core in oauth.go.
//
// A new server that answers 401 sends the user through consent once; after that the
// token lives encrypted in the secrets store under "mcp_oauth_<id>", and every
// connection or tool call refreshes it transparently when it expires.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// oauthSecretPrefix marks a server's auth_secret as "this is an OAuth token blob,
// not a raw bearer" — that one convention is how the rest of the manager tells the
// two apart, so no schema change was needed.
const oauthSecretPrefix = "mcp_oauth_"

// oauthState is the encrypted-at-rest blob per OAuth server: the discovered
// endpoints + the client we registered (so a refresh needs no re-discovery) and the
// current token.
type oauthState struct {
	Meta  *OAuthMeta    `json:"meta"`
	Token *oauth2.Token `json:"token"`
}

// pendingOAuth is a flow awaiting the user's consent redirect, keyed by the CSRF
// `state`. In-memory and short-lived — a restart drops it and the user re-clicks.
type pendingOAuth struct {
	sessionID   string
	name        string
	url         string
	meta        *OAuthMeta
	verifier    string
	redirectURI string
	created     time.Time
}

var oauthHTTP = &http.Client{Timeout: 20 * time.Second}

func randToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// StartOAuth runs discovery + dynamic client registration for a server that asked
// for OAuth, then returns the consent URL the user must open. resourceMeta is the
// pointer from the 401 (may be empty — discovery then falls back to well-known).
// The pending flow is remembered by the returned state.
func (m *Manager) StartOAuth(ctx context.Context, sessionID, name, url, resourceMeta, redirectURI string) (authorizeURL, state string, err error) {
	meta, err := discoverOAuth(ctx, oauthHTTP, resourceMeta, url)
	if err != nil {
		return "", "", err
	}
	if err := meta.registerClient(ctx, oauthHTTP, redirectURI); err != nil {
		return "", "", err
	}
	state = randToken()
	verifier := oauth2.GenerateVerifier()

	m.pendingMu.Lock()
	if m.pending == nil {
		m.pending = map[string]*pendingOAuth{}
	}
	// Drop flows older than 15 min so an abandoned consent doesn't leak.
	for k, p := range m.pending {
		if time.Since(p.created) > 15*time.Minute {
			delete(m.pending, k)
		}
	}
	m.pending[state] = &pendingOAuth{sessionID, name, url, meta, verifier, redirectURI, time.Now()}
	m.pendingMu.Unlock()

	return meta.authorizeURL(redirectURI, state, verifier), state, nil
}

// CompleteOAuth finishes the flow after the consent redirect: exchange the code,
// persist the token, then connect to the server with the bearer and cache its tools.
func (m *Manager) CompleteOAuth(ctx context.Context, state, code string) ([]Tool, error) {
	m.pendingMu.Lock()
	p := m.pending[state]
	delete(m.pending, state)
	m.pendingMu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("unknown or expired OAuth state — start the connection again")
	}
	tok, err := p.meta.exchange(ctx, oauthHTTP, p.redirectURI, code, p.verifier)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	id := slugify(p.name)
	if err := m.saveOAuthState(ctx, p.sessionID, id, &oauthState{Meta: p.meta, Token: tok}); err != nil {
		return nil, err
	}
	// Connect with the freshly-minted bearer; this lists tools and persists the row.
	return m.Connect(ctx, p.sessionID, p.name, p.url, oauthSecretPrefix+id)
}

func (m *Manager) saveOAuthState(ctx context.Context, scope, id string, st *oauthState) error {
	ms := m.getStore()
	if ms == nil {
		return fmt.Errorf("database not available")
	}
	blob, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return ms.ConfigScope(scope).SetSecret(ctx, oauthSecretPrefix+id, string(blob))
}

// loadOAuthState resolves the token blob under scope first (a group/personal
// MCP server's own tenant), falling back to the legacy unscoped lookup for a
// server connected before scoping existed — no migration needed.
func (m *Manager) loadOAuthState(ctx context.Context, scope, secretName string) (*oauthState, error) {
	ms := m.getStore()
	if ms == nil {
		return nil, fmt.Errorf("database not available")
	}
	raw, ok, err := ms.ConfigScope(scope).GetSecret(ctx, secretName)
	if err != nil {
		return nil, err
	}
	if !ok && scope != "" {
		raw, ok, err = ms.GetSecret(ctx, secretName)
		if err != nil {
			return nil, err
		}
	}
	if !ok {
		return nil, fmt.Errorf("oauth token %q not found — reconnect the server", secretName)
	}
	var st oauthState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// bearerFor resolves the Authorization header value for a server. For OAuth servers
// (auth_secret prefixed mcp_oauth_) it loads the token, refreshes and re-persists it
// when expired, and returns a fresh bearer. For everything else it's the stored
// secret as-is. Empty authSecret → no header. scope is the server's own tenant
// ("g<id>" for a group MCP server, "u<id>"/board id for a personal one, or ""
// for legacy/single-user) — resolved first, falling back to the unscoped
// global lookup so a server connected before scoping existed keeps working
// with no migration.
func (m *Manager) bearerFor(ctx context.Context, scope, authSecret string) (string, error) {
	if authSecret == "" {
		return "", nil
	}
	ms := m.getStore()
	if ms == nil {
		return "", fmt.Errorf("database not available")
	}
	if !strings.HasPrefix(authSecret, oauthSecretPrefix) {
		val, exists, err := ms.ConfigScope(scope).GetSecret(ctx, authSecret)
		if err != nil {
			return "", err
		}
		if !exists && scope != "" {
			val, exists, err = ms.GetSecret(ctx, authSecret)
			if err != nil {
				return "", err
			}
		}
		if !exists {
			return "", fmt.Errorf("secret %q not found — call request_secret first", authSecret)
		}
		return "Bearer " + val, nil
	}
	st, err := m.loadOAuthState(ctx, scope, authSecret)
	if err != nil {
		return "", err
	}
	if !st.Token.Valid() && st.Token.RefreshToken != "" {
		newTok, err := st.Meta.refresh(ctx, oauthHTTP, st.Token.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("refresh token: %w — the server may need re-authorization", err)
		}
		st.Token = newTok
		if err := m.saveOAuthState(ctx, scope, strings.TrimPrefix(authSecret, oauthSecretPrefix), st); err != nil {
			// Non-fatal: the refreshed token still works for this call. But if this
			// keeps failing, every subsequent call re-refreshes instead of reusing
			// the saved token, and burns through the refresh token — some providers
			// rotate/invalidate it on use, so this can eventually break the
			// integration. Log so it's visible instead of silently degrading.
			log.Printf("[mcp] save refreshed oauth token (scope=%s id=%s): %v", scope, strings.TrimPrefix(authSecret, oauthSecretPrefix), err)
		}
	}
	return "Bearer " + st.Token.AccessToken, nil
}
