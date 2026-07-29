package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCredsFile lays down a Claude Code credentials file, including a key we do
// not know about — the CLI stores more than we read and must get it all back.
func writeCredsFile(t *testing.T, dir string, accessToken, refreshToken string, expiresAt int64) string {
	t.Helper()
	path := filepath.Join(dir, ".credentials.json")
	doc := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      accessToken,
			"refreshToken":     refreshToken,
			"expiresAt":        expiresAt,
			"scopes":           []string{"user:inference"},
			"subscriptionType": "max",
		},
		"someOtherKeyClaudeCodeOwns": "keep me",
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokenSourceUsesAValidFileTokenWithoutRefreshing(t *testing.T) {
	dir := t.TempDir()
	future := time.Now().Add(time.Hour).UnixMilli()
	path := writeCredsFile(t, dir, "cc-still-good", "cc-refresh", future)

	ts := NewTokenSource("", path)
	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cc-still-good" {
		t.Errorf("expected the stored token, got %q", got)
	}
}

func TestTokenSourceStaticTokenWinsOverTheFile(t *testing.T) {
	dir := t.TempDir()
	path := writeCredsFile(t, dir, "cc-from-file", "cc-refresh", time.Now().Add(time.Hour).UnixMilli())

	ts := NewTokenSource("sk-ant-api03-explicit", path)
	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk-ant-api03-explicit" {
		t.Errorf("an explicitly configured key must win, got %q", got)
	}
}

func TestTokenSourceRefreshesAnExpiredTokenAndPersistsTheRotation(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotForm = string(buf)
		if ua := r.Header.Get("User-Agent"); ua != tokenUserAgent {
			t.Errorf("the token endpoint throttles claude-code user-agents; got %q", ua)
		}
		fmt.Fprint(w, `{"access_token":"cc-fresh","refresh_token":"cc-rotated","expires_in":3600}`)
	}))
	defer srv.Close()

	old := tokenEndpoints
	tokenEndpoints = []string{srv.URL}
	defer func() { tokenEndpoints = old }()

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).UnixMilli()
	path := writeCredsFile(t, dir, "cc-expired", "cc-old-refresh", past)

	ts := NewTokenSource("", path)
	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if got != "cc-fresh" {
		t.Errorf("expected the refreshed token, got %q", got)
	}
	if !strings.Contains(gotForm, "grant_type=refresh_token") || !strings.Contains(gotForm, oauthClientID) {
		t.Errorf("unexpected refresh request body: %q", gotForm)
	}

	// Anthropic rotates the refresh token on use. Failing to persist it would
	// leave the next Prism start replaying a credential that no longer works.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the credentials file is no longer valid JSON: %v", err)
	}
	nested, _ := doc["claudeAiOauth"].(map[string]any)
	if nested["refreshToken"] != "cc-rotated" {
		t.Errorf("the rotated refresh token was not persisted: %v", nested["refreshToken"])
	}
	if nested["accessToken"] != "cc-fresh" {
		t.Errorf("the fresh access token was not persisted: %v", nested["accessToken"])
	}
	if nested["subscriptionType"] != "max" {
		t.Error("fields we do not read must survive the write — the file is Claude Code's")
	}
	if doc["someOtherKeyClaudeCodeOwns"] != "keep me" {
		t.Error("top-level keys we do not read must survive the write")
	}

	// A second call must reuse the refreshed token rather than refresh again.
	gotForm = ""
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if gotForm != "" {
		t.Error("a still-valid token must not trigger another refresh")
	}
}

func TestWriteCredentialsFallsBackWhenRenameIsImpossible(t *testing.T) {
	// Stands in for the bind-mounted file in Docker, where rename onto the mount
	// point fails: the write must still land, or the rotation is lost.
	dir := t.TempDir()
	path := writeCredsFile(t, dir, "cc-old", "cc-old-refresh", 1)

	// A directory where CreateTemp cannot work forces the atomic path to fail
	// without making the target itself unwritable.
	if err := os.Chmod(dir, 0500); err != nil {
		t.Skipf("cannot make the directory read-only here: %v", err)
	}
	defer os.Chmod(dir, 0700)
	if err := writeAtomic(path, []byte("{}")); err == nil {
		t.Skip("this filesystem still allows the atomic path; nothing to assert")
	}

	if err := writeCredentials(path, credentials{AccessToken: "cc-new", RefreshToken: "cc-new-refresh", ExpiresAt: 2}); err != nil {
		t.Fatalf("the fallback write should succeed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the file is no longer valid JSON: %v", err)
	}
	nested, _ := doc["claudeAiOauth"].(map[string]any)
	if nested["accessToken"] != "cc-new" {
		t.Errorf("the refreshed token did not land: %v", nested["accessToken"])
	}
	if nested["subscriptionType"] != "max" {
		t.Error("fields we do not read must survive the fallback write too")
	}
}

func TestTokenSourceReportsAnUnrefreshableExpiredToken(t *testing.T) {
	dir := t.TempDir()
	path := writeCredsFile(t, dir, "cc-expired", "", time.Now().Add(-time.Hour).UnixMilli())

	ts := NewTokenSource("", path)
	_, err := ts.Token(context.Background())
	if err == nil {
		t.Fatal("an expired token with no refresh token cannot be recovered silently")
	}
}

func TestCredentialsValidity(t *testing.T) {
	if (credentials{}).valid() {
		t.Error("no access token is never valid")
	}
	// Managed keys record no expiry.
	if !(credentials{AccessToken: "x"}).valid() {
		t.Error("a token with no recorded expiry should be usable")
	}
	// Expiring inside the skew window counts as expired, so a token cannot go
	// stale between the check and the request landing.
	almost := credentials{AccessToken: "x", ExpiresAt: time.Now().Add(refreshSkew / 2).UnixMilli()}
	if almost.valid() {
		t.Error("a token expiring within the skew window must be refreshed early")
	}
}
