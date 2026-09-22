package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateKeyMigration(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "workspace", ".secret_key")
	private := filepath.Join(root, "private", "key")
	os.MkdirAll(filepath.Dir(legacy), 0755)
	old, err := LoadOrGenerateKey(legacy)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := encryptValue(old, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	key, err := LoadPrivateKey(private, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, old) {
		t.Fatal("key changed")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy key remains in workspace")
	}
	value, err := decryptValue(key, ciphertext)
	if err != nil || value != "fixture" {
		t.Fatal("existing secrets unreadable")
	}
	again, err := LoadPrivateKey(private, legacy)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("migration not idempotent")
	}
	if _, err := LoadOrGenerateKey(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(private, legacy); err == nil {
		t.Fatal("conflicting keys accepted")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("conflicting legacy key destroyed")
	}
}
func TestOAuthCredentialsAreIntegrationSecrets(t *testing.T) {
	for _, name := range []string{"oauth_google_token", "oauth_google_client_secret", "oauth_microsoft_token", "oauth_microsoft_client_secret"} {
		if !IsIntegrationSecret(name) {
			t.Fatal(name)
		}
		if ValidateScriptSecretName(name) == nil {
			t.Fatal("reserved key accepted", name)
		}
	}
}
