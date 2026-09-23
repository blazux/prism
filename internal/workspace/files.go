// Package workspace confines server-side file access to the execution volume.
// os.Root enforces the boundary during the operation, including symlink races.
package workspace

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func Name(name string) (string, error) {
	name = strings.TrimPrefix(name, "/workspace/")
	if name == "/workspace" {
		name = "."
	}
	name = filepath.Clean(name)
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("path must stay inside the workspace")
	}
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		if part == ".secret_key" || strings.HasPrefix(part, ".secret-key-") {
			return "", fmt.Errorf("private server key is not a workspace document")
		}
	}
	return name, nil
}
func protected(root *os.Root, f *os.File) bool {
	key, err := root.Stat(".secret_key")
	if err != nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && os.SameFile(key, info)
}
func Open(dir, name string) (*os.File, error) {
	name, err := Name(name)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	if protected(root, f) {
		f.Close()
		return nil, fmt.Errorf("private server key is not a workspace document")
	}
	return f, nil
}
func ReadFile(dir, name string) ([]byte, error) {
	f, err := Open(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
func Write(dir, name string, src io.Reader) (int64, error) {
	name, err := Name(name)
	if err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return 0, err
	}
	// Do not truncate until the target has been checked for aliases of the key.
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if protected(root, f) {
		return 0, fmt.Errorf("private server key is not a workspace document")
	}
	if err = f.Truncate(0); err != nil {
		return 0, err
	}
	return io.Copy(f, src)
}
func WriteFile(dir, name string, data []byte) error {
	_, err := Write(dir, name, strings.NewReader(string(data)))
	return err
}
func Remove(dir, name string) error {
	name, err := Name(name)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(name)
}
func ReadDir(dir, name string) ([]os.DirEntry, error) {
	f, err := Open(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// FS is used only for authenticated generated content. It does not allow links
// outside its root; directory listings are disabled (files remain addressable).
type FS string

func (dir FS) Open(name string) (http.File, error) {
	f, err := Open(string(dir), strings.TrimPrefix(name, "/"))
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

// SubFS anchors a generated-content directory at its trusted workspace parent.
type SubFS struct{ Root, Prefix string }

func (fs SubFS) Open(name string) (http.File, error) {
	clean, err := Name(strings.TrimPrefix(name, "/"))
	if err != nil {
		return nil, err
	}
	return FS(fs.Root).Open(filepath.Join(fs.Prefix, clean))
}

// MkdirAll creates directories without following links outside the volume.
func MkdirAll(dir, name string, mode os.FileMode) error {
	name, err := Name(name)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.MkdirAll(name, mode)
}
