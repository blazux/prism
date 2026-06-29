// Package oauthx is a small "bring your own OAuth app" layer. Prism never hosts
// shared Google/Microsoft apps — the user creates their own and pastes the
// client id/secret. This package runs the authorization-code flow, stores and
// refreshes tokens (in the encrypted secrets store), and hands out an
// *http.Client that injects a fresh bearer token.
package oauthx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"

	"prism/internal/memory"
)

type Provider struct {
	Name     string
	AuthURL  string
	TokenURL string
	Scopes   []string
}

// Providers is the registry of supported OAuth providers.
var Providers = map[string]Provider{
	"google": {
		Name:     "google",
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
		Scopes:   []string{"https://www.googleapis.com/auth/calendar"},
	},
	"microsoft": {
		Name:     "microsoft",
		AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		Scopes:   []string{"offline_access", "https://graph.microsoft.com/Calendars.ReadWrite"},
	},
}

func clientIDKey(name string) string     { return "oauth_" + name + "_client_id" }
func clientSecretKey(name string) string { return "oauth_" + name + "_client_secret" }
func tokenKey(name string) string        { return "oauth_" + name + "_token" }

func oauthConfig(p Provider, id, secret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     id,
		ClientSecret: secret,
		Endpoint:     oauth2.Endpoint{AuthURL: p.AuthURL, TokenURL: p.TokenURL},
		RedirectURL:  redirectURL,
		Scopes:       p.Scopes,
	}
}

// ─── client credentials (user-supplied) ─────────────────────────────────────────

func SaveClient(ctx context.Context, store *memory.Store, name, id, secret string) error {
	if err := store.SetConfig(ctx, clientIDKey(name), id); err != nil {
		return err
	}
	return store.SetSecret(ctx, clientSecretKey(name), secret)
}

func ClientID(ctx context.Context, store *memory.Store, name string) string {
	id, _, _ := store.GetConfig(ctx, clientIDKey(name))
	return id
}

func clientCreds(ctx context.Context, store *memory.Store, name string) (id, secret string, ok bool) {
	id, _, _ = store.GetConfig(ctx, clientIDKey(name))
	secret, _, _ = store.GetSecret(ctx, clientSecretKey(name))
	return id, secret, id != "" && secret != ""
}

// ─── tokens ─────────────────────────────────────────────────────────────────────

func saveToken(ctx context.Context, store *memory.Store, name string, tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return store.SetSecret(ctx, tokenKey(name), string(b))
}

func loadToken(ctx context.Context, store *memory.Store, name string) (*oauth2.Token, bool) {
	raw, ok, _ := store.GetSecret(ctx, tokenKey(name))
	if !ok || raw == "" {
		return nil, false
	}
	var tok oauth2.Token
	if json.Unmarshal([]byte(raw), &tok) != nil || tok.RefreshToken == "" && tok.AccessToken == "" {
		return nil, false
	}
	return &tok, true
}

// Connected reports whether a usable token is stored for the provider.
func Connected(ctx context.Context, store *memory.Store, name string) bool {
	_, ok := loadToken(ctx, store, name)
	return ok
}

func Disconnect(ctx context.Context, store *memory.Store, name string) error {
	return store.SetSecret(ctx, tokenKey(name), "")
}

// ─── flow ───────────────────────────────────────────────────────────────────────

// AuthCodeURL builds the provider consent URL for the given redirect + state.
func AuthCodeURL(ctx context.Context, store *memory.Store, name, redirectURL, state string) (string, error) {
	p, ok := Providers[name]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", name)
	}
	id, secret, ok := clientCreds(ctx, store, name)
	if !ok {
		return "", fmt.Errorf("client id/secret not set")
	}
	cfg := oauthConfig(p, id, secret, redirectURL)
	// Offline access + forced consent so we reliably receive a refresh token.
	return cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	), nil
}

// Exchange swaps an auth code for a token and stores it.
func Exchange(ctx context.Context, store *memory.Store, name, redirectURL, code string) error {
	p, ok := Providers[name]
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	id, secret, ok := clientCreds(ctx, store, name)
	if !ok {
		return fmt.Errorf("client id/secret not set")
	}
	cfg := oauthConfig(p, id, secret, redirectURL)
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return err
	}
	return saveToken(ctx, store, name, tok)
}

// HTTPClient returns an *http.Client that injects (and refreshes) the bearer
// token, persisting refreshed tokens back to the store.
func HTTPClient(ctx context.Context, store *memory.Store, name string) (*http.Client, error) {
	p, ok := Providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	id, secret, ok := clientCreds(ctx, store, name)
	if !ok {
		return nil, fmt.Errorf("%s OAuth not configured", name)
	}
	tok, ok := loadToken(ctx, store, name)
	if !ok {
		return nil, fmt.Errorf("%s not connected", name)
	}
	cfg := oauthConfig(p, id, secret, "")
	ts := &persistingSource{ctx: ctx, store: store, name: name, base: cfg.TokenSource(ctx, tok), last: tok}
	return oauth2.NewClient(ctx, ts), nil
}

// persistingSource saves the token whenever oauth2 refreshes it.
type persistingSource struct {
	ctx   context.Context
	store *memory.Store
	name  string
	base  oauth2.TokenSource
	last  *oauth2.Token
}

func (s *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	if s.last == nil || tok.AccessToken != s.last.AccessToken || tok.RefreshToken != s.last.RefreshToken {
		_ = saveToken(s.ctx, s.store, s.name, tok)
		s.last = tok
	}
	return tok, nil
}
