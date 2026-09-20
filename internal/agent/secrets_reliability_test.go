package agent

import (
	"context"
	"strings"
	"testing"
)

func TestScriptSecretsEnv(t *testing.T) {
	env, err := scriptSecretsEnv(map[string]string{"api-key": "fixture", "email_password": "hidden", "MCP_OAUTH_ACCESS": "hidden"})
	if err != nil || len(env) != 1 || env["API_KEY"] != "fixture" {
		t.Fatal("unexpected environment composition", err)
	}
	for _, secrets := range []map[string]string{
		{"my-key": "first", "my_key": "second"},
		{"prism_token": "fake-token"},
		{"123": "bad-name"},
	} {
		if env, err := scriptSecretsEnv(secrets); err == nil || env != nil {
			t.Fatal("invalid secrets must stop execution")
		}
	}
}

func TestRequestSecretUnavailableOrReservedDoesNotPrompt(t *testing.T) {
	e := &ToolExecutor{}
	called := false
	e.SetSecretRequestCallback(func(context.Context, string, string) error { called = true; return nil })
	for _, name := range []string{"github_token", "email_password", "PRISM_TOKEN", "  "} {
		result, err := e.requestSecret(context.Background(), name, "test")
		if err == nil || strings.Contains(result, "stored securely") {
			t.Errorf("request %q falsely succeeded", name)
		}
	}
	if called {
		t.Fatal("prompted without an available store")
	}
}
