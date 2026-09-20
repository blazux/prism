package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSecretKeyCreateAndReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".secret_key")
	key, err := LoadOrGenerateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrGenerateKey(path)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("key changed on reload", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions: %v", info.Mode())
	}
	ciphertext, err := encryptValue(key, "test credential")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := decryptValue(again, ciphertext)
	if err != nil || plaintext != "test credential" {
		t.Fatal("round trip failed", err)
	}
	wrong := bytes.Repeat([]byte{7}, 32)
	if _, err := decryptValue(wrong, ciphertext); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestSecretKeyUnreadableIsNeverReplaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses read permission checks")
	}
	path := filepath.Join(t.TempDir(), ".secret_key")
	original, err := LoadOrGenerateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0200); err != nil {
		t.Fatal(err)
	}
	_, loadErr := LoadOrGenerateKey(path)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if loadErr == nil {
		t.Fatal("unreadable existing key must fail")
	}
	again, err := LoadOrGenerateKey(path)
	if err != nil || !bytes.Equal(original, again) {
		t.Fatal("existing key was modified", err)
	}
}

func TestSecretKeyCorruptAndDanglingLinkArePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt")
	if err := os.WriteFile(path, []byte("invalid key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrGenerateKey(path); err == nil {
		t.Fatal("corrupt key accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != "invalid key" {
		t.Fatal("corrupt file overwritten", err)
	}
	link, target := filepath.Join(dir, "link"), filepath.Join(dir, "missing")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrGenerateKey(link); err == nil {
		t.Fatal("dangling key symlink accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("dangling link target was created")
	}
}

func TestSecretKeyConcurrentCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".secret_key")
	var wg sync.WaitGroup
	keys := make([][]byte, 24)
	errs := make([]error, len(keys))
	for i := range keys {
		wg.Add(1)
		go func(i int) { defer wg.Done(); keys[i], errs[i] = LoadOrGenerateKey(path) }(i)
	}
	wg.Wait()
	for i := range keys {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if !bytes.Equal(keys[0], keys[i]) {
			t.Fatal("concurrent starts obtained different keys")
		}
	}
}
