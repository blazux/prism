package mcp

// OAuth 2.1 for remote MCP servers (the flow Asana, Linear, Notion… require).
//
// A protected server answers an unauthenticated request with 401 and a
// WWW-Authenticate: Bearer challenge pointing at its protected-resource metadata
// (RFC 9728). From there we find the authorization server (RFC 8414), register a
// public client on the fly (RFC 7591 dynamic client registration, so the user
// never pre-registers an app), and run authorization-code + PKCE with the RFC 8707
// `resource` indicator. Token mechanics ride on golang.org/x/oauth2; only the
// discovery and registration are hand-rolled, because oauth2 has no notion of them.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/oauth2"
)

// OAuthRequiredError is returned when a server answers 401 with a Bearer challenge:
// it means "authenticate with OAuth first". ResourceMetadata is the RFC 9728 URL the
// challenge pointed at (empty if the server 401'd without one — discovery then falls
// back to the well-known path).
type OAuthRequiredError struct{ ResourceMetadata string }

func (e *OAuthRequiredError) Error() string {
	return "MCP server requires OAuth authorization"
}

// OAuthMeta is everything discovery + registration yields for one server: where to
// send the user, where to swap the code, and the client_id we registered. It is
// what we persist alongside the token so a refresh needs no re-discovery.
type OAuthMeta struct {
	Resource              string   `json:"resource"` // RFC 8707 indicator — the value the AS expects, from the resource metadata
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	Scopes                []string `json:"scopes,omitempty"`
	ClientID              string   `json:"client_id"`
}

var reResourceMetadata = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// resourceMetadataFromChallenge pulls the resource_metadata URL out of a
// WWW-Authenticate: Bearer challenge. ok is false when the header carries none.
func resourceMetadataFromChallenge(header string) (string, bool) {
	m := reResourceMetadata.FindStringSubmatch(header)
	if len(m) == 2 {
		return m[1], true
	}
	return "", false
}

// originOf returns scheme://host for a URL, used to guess the well-known location
// when a server 401s without a resource_metadata pointer.
func originOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return strings.TrimRight(raw, "/")
}

func getJSON(ctx context.Context, hc *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("GET %s → HTTP %d: %s", u, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// discoverOAuth walks RFC 9728 → RFC 8414. resourceMetaURL is what the 401 pointed
// at (empty falls back to the server's well-known); serverURL is the MCP endpoint,
// used as the fallback discovery base and the default resource indicator.
func discoverOAuth(ctx context.Context, hc *http.Client, resourceMetaURL, serverURL string) (*OAuthMeta, error) {
	if resourceMetaURL == "" {
		resourceMetaURL = originOf(serverURL) + "/.well-known/oauth-protected-resource"
	}
	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := getJSON(ctx, hc, resourceMetaURL, &prm); err != nil {
		return nil, fmt.Errorf("protected-resource metadata: %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("resource metadata lists no authorization_servers")
	}
	authServer := strings.TrimRight(prm.AuthorizationServers[0], "/")
	resource := prm.Resource
	if resource == "" {
		resource = serverURL
	}

	var asm struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		RegistrationEndpoint  string `json:"registration_endpoint"`
	}
	if err := getJSON(ctx, hc, authServer+"/.well-known/oauth-authorization-server", &asm); err != nil {
		return nil, fmt.Errorf("authorization-server metadata: %w", err)
	}
	if asm.AuthorizationEndpoint == "" || asm.TokenEndpoint == "" {
		return nil, fmt.Errorf("authorization server advertises no authorize/token endpoint")
	}
	return &OAuthMeta{
		Resource:              resource,
		AuthorizationEndpoint: asm.AuthorizationEndpoint,
		TokenEndpoint:         asm.TokenEndpoint,
		RegistrationEndpoint:  asm.RegistrationEndpoint,
		Scopes:                prm.ScopesSupported,
	}, nil
}

// registerClient does RFC 7591 dynamic client registration and returns a public
// (PKCE, no secret) client_id. Sets ClientID on m.
func (m *OAuthMeta) registerClient(ctx context.Context, hc *http.Client, redirectURI string) error {
	if m.RegistrationEndpoint == "" {
		return fmt.Errorf("server does not support dynamic client registration — a client_id must be provided manually")
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "Prism",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", m.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("dynamic client registration → HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.ClientID == "" {
		return fmt.Errorf("registration returned no client_id")
	}
	m.ClientID = out.ClientID
	return nil
}

// oauth2Config is the shared config for authorize/exchange/refresh. Public client:
// no secret, PKCE carries the proof.
func (m *OAuthMeta) oauth2Config(redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:    m.ClientID,
		RedirectURL: redirectURI,
		Scopes:      m.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   m.AuthorizationEndpoint,
			TokenURL:  m.TokenEndpoint,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// authorizeURL builds the consent URL with PKCE (S256) and the RFC 8707 resource
// indicator, which Asana and others require.
func (m *OAuthMeta) authorizeURL(redirectURI, state, verifier string) string {
	return m.oauth2Config(redirectURI).AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("resource", m.Resource),
	)
}

// exchange swaps the authorization code for a token, carrying the PKCE verifier
// and the resource indicator on the token request.
func (m *OAuthMeta) exchange(ctx context.Context, hc *http.Client, redirectURI, code, verifier string) (*oauth2.Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	return m.oauth2Config(redirectURI).Exchange(ctx, code,
		oauth2.VerifierOption(verifier),
		oauth2.SetAuthURLParam("resource", m.Resource),
	)
}

// refresh mints a fresh token from a stored refresh token. Returns the new token
// (its RefreshToken may rotate — persist whatever comes back).
func (m *OAuthMeta) refresh(ctx context.Context, hc *http.Client, refreshToken string) (*oauth2.Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	src := m.oauth2Config("").TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	return src.Token()
}
