package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"prism/internal/agent"
	"prism/internal/memory"
)

func TestSecretsLiveReliability(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("requires disposable PRISM_TEST_SECRETS_URL")
	}
	ctx := context.Background()
	// Isolate each run, and remove only the schema created by this test.
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	schema := fmt.Sprintf("secrets_regression_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema+",public")
	parsed.RawQuery = query.Encode()
	dsn = parsed.String()
	ms, err := memory.NewStore(ctx, dsn, bytes.Repeat([]byte{3}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	s := &Server{memStore: ms}
	call := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		switch {
		case path == "/api/secrets":
			s.handleSecrets(w, r)
		case path == "/api/user/secrets":
			s.handleUserSecrets(w, r)
		case strings.HasPrefix(path, "/api/user/secrets/"):
			s.handleUserSecretByName(w, r)
		default:
			s.handleSecretByName(w, r)
		}
		if w.Code != status {
			t.Fatalf("%s %s returned %d, expected %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	call("POST", "/api/secrets", `{"name":"MY_API_KEY","value":"fixture-one"}`, 200)
	call("POST", "/api/user/secrets", `{"name":"my-api-key","value":"fixture-two"}`, 400)
	call("POST", "/api/secrets", `{"name":"MY_API_KEY","value":"fixture-rotated"}`, 200)
	if v, ok, err := ms.GetSecret(ctx, "MY_API_KEY"); err != nil || !ok || v != "fixture-rotated" {
		t.Fatal("exact-name rotation failed", err)
	}
	for _, name := range []string{"PRISM_TOKEN", "Prism-Session", "email_password", "EMAIL-PASSWORD", "123", "u2:token"} {
		body, _ := json.Marshal(map[string]string{"name": name, "value": "fixture"})
		call("POST", "/api/secrets", string(body), 400)
		call("POST", "/api/user/secrets", string(body), 400)
	}
	if err := ms.SetSecret(ctx, "email_password", "fixture-integration"); err != nil {
		t.Fatal(err)
	}
	if err := ms.ConfigScope("u2").SetScriptSecret(ctx, "private_key", "fixture-personal"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/secrets", "/api/user/secrets"} {
		w := call("GET", path, "", 200)
		if !strings.Contains(w.Body.String(), "MY_API_KEY") || strings.Contains(w.Body.String(), "email_password") || strings.Contains(w.Body.String(), "private_key") {
			t.Fatal("list leaked integration/scoped names or lost mono-user names")
		}
	}
	for _, path := range []string{"/api/secrets/email_password", "/api/user/secrets/email_password"} {
		call("GET", path, "", 403)
		call("DELETE", path, "", 403)
	}
	e := agent.NewToolExecutor(nil, "", "", "", "")
	e.SetMemoryStore(ms)
	prompted := false
	e.SetSecretRequestCallback(func(context.Context, string, string) error { prompted = true; return nil })
	result, _, err := e.Execute(ctx, "request_secret", json.RawMessage(`{"name":"MY_API_KEY","description":"test"}`))
	if err != nil || prompted || !strings.Contains(result, "already stored") {
		t.Fatal("existing UI name not recognized by agent", err)
	}
	_, _, err = e.Execute(ctx, "request_secret", json.RawMessage(`{"name":"my_api_key","description":"test"}`))
	if err == nil || prompted {
		t.Fatal("alias should fail before prompting")
	}
	result, _, err = e.Execute(ctx, "secrets", json.RawMessage(`{"action":"list"}`))
	if err != nil || strings.Contains(result, "email_password") || strings.Contains(result, "private_key") {
		t.Fatal("agent list exposes integration/scoped names", err)
	}
	// Concurrent submissions cannot create two aliases. Independent scopes can.
	names := []string{"concurrent-key", "CONCURRENT_KEY", "concurrent.key"}
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) { defer wg.Done(); errs[i] = ms.SetScriptSecret(ctx, name, "fixture") }(i, name)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, memory.ErrSecretName) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent aliases: %d accepted", successes)
	}
	for _, scope := range []string{"u2", "g3"} {
		if err := ms.ConfigScope(scope).SetScriptSecret(ctx, "my_api_key", "fixture-scoped"); err != nil {
			t.Fatal("scopes should remain independent", err)
		}
	}
	// Break only a synthetic credential, then exercise HTTP and tool paths.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE secrets SET value='not-ciphertext' WHERE name='MY_API_KEY'`); err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/secrets/MY_API_KEY", "", 500)
	call("GET", "/api/user/secrets/MY_API_KEY", "", 500)
	_, _, err = e.Execute(ctx, "request_secret", json.RawMessage(`{"name":"MY_API_KEY","description":"test"}`))
	if err == nil || prompted {
		t.Fatal("decryption failure must not open a replacement prompt")
	}
	// nil Docker manager would panic if exec were reached: fail before execution.
	_, _, err = e.Execute(ctx, "exec_command", json.RawMessage(`{"command":"echo should-not-run"}`))
	if err == nil || !strings.Contains(err.Error(), "load secrets") {
		t.Fatal("failed secret loading must stop execution", err)
	}
}

func TestSecretSubmissionWithoutStoreFails(t *testing.T) {
	s := &Server{}
	err := s.saveRequestedSecret(context.Background(), "", "api_key", "fixture-only")
	if err == nil || !strings.Contains(err.Error(), "not saved") || strings.Contains(err.Error(), "fixture-only") {
		t.Fatal("missing store must report failure without exposing the value")
	}
}
