package memory

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// LoadPrivateKey moves the existing key without changing its bytes. The private
// location is persisted before removing the legacy workspace copy. A mismatch
// fails closed rather than replacing a key and losing encrypted data.
func LoadPrivateKey(path, legacy string) ([]byte, error) {
	if filepath.Clean(path) == filepath.Clean(legacy) {
		return nil, fmt.Errorf("secret key must be outside the execution workspace")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	data, oldErr := os.ReadFile(legacy)
	if oldErr != nil && !os.IsNotExist(oldErr) {
		return nil, fmt.Errorf("read legacy key: %w", oldErr)
	}
	if oldErr == nil {
		oldKey, err := decodeKey(legacy, data)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			f, err := os.CreateTemp(filepath.Dir(path), ".key-migrate-*")
			if err != nil {
				return nil, err
			}
			defer os.Remove(f.Name())
			if _, err = f.Write(data); err != nil {
				f.Close()
				return nil, err
			}
			if err = f.Sync(); err != nil {
				f.Close()
				return nil, err
			}
			if err = f.Close(); err != nil {
				return nil, err
			}
			if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
				return nil, err
			}
		}
		key, err := LoadOrGenerateKey(path)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(key, oldKey) {
			return nil, fmt.Errorf("private and legacy keys differ; restore the matching key before starting")
		}
		// Flush the containing directory before retiring the only legacy copy.
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return nil, err
		}
		if err = os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove legacy workspace key: %w", err)
		}
		return key, nil
	}
	return LoadOrGenerateKey(path)
}
