package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A Claude Pro/Max subscription grants no API access: there is no key to paste,
// only the OAuth token the Claude Code CLI obtains when you log in. That token
// is what this file resolves, keeps fresh, and hands to the chat client.
//
// The client id and token endpoints below are Claude Code's own — the token is
// only accepted by Anthropic when the request also carries Claude Code's
// fingerprint (see claudeCodeUserAgent in client.go). Using a subscription this
// way is outside Anthropic's terms of service; it is a deliberate choice made
// when this backend was added, not an oversight.
const oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// Anthropic moved the token endpoint to platform.claude.com; console.anthropic.com
// still answers and is kept as a fallback for the day the new host is the one that
// breaks.
var tokenEndpoints = []string{
	"https://platform.claude.com/v1/oauth/token",
	"https://console.anthropic.com/v1/oauth/token",
}

// tokenUserAgent is sent to the *token* endpoint only, and is deliberately not
// the claude-code UA used for inference: Anthropic rate-limits (429) token
// requests whose user-agent starts with "claude-code/". The CLI itself exchanges
// codes with a bare axios client, so we look like that here.
const tokenUserAgent = "axios/1.7.9"

// refreshSkew refreshes slightly before the real expiry so a token cannot go
// stale between the check and the request reaching Anthropic.
const refreshSkew = 60 * time.Second

// credentials mirrors the claudeAiOauth object inside Claude Code's credentials
// file. ExpiresAt is milliseconds since the epoch; 0 means "no expiry recorded",
// which is how managed keys show up.
type credentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"`
}

func (c credentials) valid() bool {
	if c.AccessToken == "" {
		return false
	}
	if c.ExpiresAt == 0 {
		return true
	}
	return time.Now().Add(refreshSkew).UnixMilli() < c.ExpiresAt
}

// TokenSource yields a usable Anthropic credential, refreshing the subscription
// OAuth token when it has expired. It is safe for concurrent use: every chat
// turn asks it for a token.
type TokenSource struct {
	static string // explicit token from config; short-circuits everything else
	path   string // Claude Code credentials file

	mu     sync.Mutex
	creds  credentials
	client *http.Client
}

// DefaultCredentialsPath is where the Claude Code CLI stores its OAuth tokens.
func DefaultCredentialsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

// NewTokenSource builds a token source. static is an explicit token (a console
// API key, or a `claude setup-token` OAuth token) and wins when set; otherwise
// the Claude Code credentials file at path is read and refreshed as needed. An
// empty path falls back to DefaultCredentialsPath.
func NewTokenSource(static, path string) *TokenSource {
	if path == "" {
		path = DefaultCredentialsPath()
	}
	return &TokenSource{
		static: strings.TrimSpace(static),
		path:   path,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Token returns the credential to authenticate the next request with.
func (t *TokenSource) Token(ctx context.Context) (string, error) {
	if t.static != "" {
		return t.static, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.creds.valid() {
		return t.creds.AccessToken, nil
	}

	// Re-read on every miss rather than caching the file: Claude Code refreshes
	// the same credentials on its own schedule, and picking up its fresher token
	// avoids a refresh (and a refresh-token rotation) we don't need to do.
	if fileCreds, err := readCredentials(t.path); err == nil {
		t.creds = fileCreds
		if t.creds.valid() {
			return t.creds.AccessToken, nil
		}
	} else if t.creds.RefreshToken == "" {
		return "", fmt.Errorf("no Anthropic credentials: %w (log in with `claude` or set ANTHROPIC_API_KEY)", err)
	}

	if t.creds.RefreshToken == "" {
		return "", fmt.Errorf("Anthropic token in %s has expired and carries no refresh token — run `claude` to log in again", t.path)
	}

	refreshed, err := t.refresh(ctx, t.creds.RefreshToken)
	if err != nil {
		return "", err
	}
	t.creds = refreshed
	// Persist so the next Prism start doesn't replay a refresh token Anthropic
	// has already rotated away — that would leave the backend unable to
	// authenticate at all until the user logs in again.
	if err := writeCredentials(t.path, refreshed); err != nil {
		log.Printf("[anthropic] refreshed the OAuth token but could not write %s: %v", t.path, err)
	}
	return t.creds.AccessToken, nil
}

func (t *TokenSource) refresh(ctx context.Context, refreshToken string) (credentials, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {oauthClientID},
	}.Encode()

	var lastErr error
	for _, endpoint := range tokenEndpoints {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form))
		if err != nil {
			return credentials{}, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", tokenUserAgent)

		resp, err := t.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := readLimited(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("token endpoint %s returned %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
			continue
		}

		var payload struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			lastErr = fmt.Errorf("token endpoint %s returned unparseable JSON: %w", endpoint, err)
			continue
		}
		if payload.AccessToken == "" {
			lastErr = fmt.Errorf("token endpoint %s returned no access token", endpoint)
			continue
		}

		out := credentials{
			AccessToken:  payload.AccessToken,
			RefreshToken: payload.RefreshToken,
		}
		if out.RefreshToken == "" {
			out.RefreshToken = refreshToken // endpoint kept the old one alive
		}
		if payload.ExpiresIn > 0 {
			out.ExpiresAt = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).UnixMilli()
		}
		return out, nil
	}

	return credentials{}, fmt.Errorf("could not refresh the Anthropic OAuth token: %w", lastErr)
}

func readCredentials(path string) (credentials, error) {
	if path == "" {
		return credentials{}, fmt.Errorf("no credentials path configured")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, err
	}
	var file struct {
		ClaudeAIOAuth credentials `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return credentials{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if file.ClaudeAIOAuth.AccessToken == "" {
		return credentials{}, fmt.Errorf("%s has no claudeAiOauth.accessToken", path)
	}
	return file.ClaudeAIOAuth, nil
}

// writeCredentials updates the three OAuth fields in place, leaving every other
// key in the file untouched — it is Claude Code's file, and it stores more than
// we read (scopes, subscription type). The write is atomic so a crash mid-write
// cannot leave the CLI without credentials.
func writeCredentials(path string, creds credentials) error {
	if path == "" {
		return fmt.Errorf("no credentials path configured")
	}
	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	nested, _ := doc["claudeAiOauth"].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
	}
	nested["accessToken"] = creds.AccessToken
	nested["refreshToken"] = creds.RefreshToken
	nested["expiresAt"] = creds.ExpiresAt
	doc["claudeAiOauth"] = nested

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// isOAuthToken separates subscription/OAuth tokens, which authenticate with a
// bearer header and Claude Code's fingerprint, from ordinary console API keys,
// which use x-api-key and must NOT carry that fingerprint.
func isOAuthToken(token string) bool {
	switch {
	case token == "":
		return false
	case strings.HasPrefix(token, "sk-ant-api"): // console API key
		return false
	case strings.HasPrefix(token, "sk-ant-"): // setup-token, managed key
		return true
	case strings.HasPrefix(token, "eyJ"): // JWT from the OAuth flow
		return true
	case strings.HasPrefix(token, "cc-"): // Claude Code access token
		return true
	default:
		return false
	}
}
