package memory

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrGenerateKey reads a 32-byte AES-256 key from path (base64-encoded),
// creating and persisting a random one if the file does not exist.
func LoadOrGenerateKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return decodeKey(path, data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret key: %w", err)
	}
	// A dangling symlink is not a missing key file. Never replace it.
	if _, err := os.Lstat(path); err == nil {
		// A concurrent creator may have published a complete key since ReadFile.
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read existing secret key: %w", err)
		}
		return decodeKey(path, data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect secret key: %w", err)
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"
	// Publish a complete, flushed file without overwriting a key created by
	// another process. A failed write leaves the destination untouched.
	f, err := os.CreateTemp(filepath.Dir(path), ".secret-key-*")
	if err != nil {
		return nil, fmt.Errorf("create key file: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.WriteString(encoded); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("sync key file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close key file: %w", err)
	}
	if err := os.Link(f.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, fmt.Errorf("read existing secret key: %w", readErr)
			}
			return decodeKey(path, data)
		}
		return nil, fmt.Errorf("publish key file: %w", err)
	}

	return key, nil
}

// encryptValue encrypts plaintext with AES-256-GCM and returns a base64-encoded ciphertext.
// The random nonce is prepended to the ciphertext before encoding.
func encryptValue(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptValue decodes and decrypts a value produced by encryptValue.
func decryptValue(key []byte, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	plaintext, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

func decodeKey(path string, data []byte) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("secret key file %s is corrupt (expected 32-byte base64); restore the original key from backup", path)
	}
	return key, nil
}
