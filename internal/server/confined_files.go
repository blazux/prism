package server

import (
	"os"
	"path/filepath"
	"prism/internal/workspace"
	"strings"
)

// Always anchor at the trusted volume, never at an agent-writable subdirectory.
// A separately configured plugin volume remains supported for standalone Prism.
func (s *Server) fileRoot(full string) (string, string, error) {
	if s.cfg.WorkspaceDir != "" {
		rel, err := filepath.Rel(s.cfg.WorkspaceDir, full)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return s.cfg.WorkspaceDir, rel, nil
		}
	}
	root := filepath.Dir(s.cfg.PluginDir)
	rel, err := filepath.Rel(root, full)
	return root, rel, err
}
func (s *Server) readManagedFile(full string) ([]byte, error) {
	root, rel, err := s.fileRoot(full)
	if err != nil {
		return nil, err
	}
	return workspace.ReadFile(root, rel)
}
func (s *Server) writeManagedFile(full string, data []byte) error {
	root, rel, err := s.fileRoot(full)
	if err != nil {
		return err
	}
	return workspace.WriteFile(root, rel, data)
}
func (s *Server) readManagedDir(full string) ([]os.DirEntry, error) {
	root, rel, err := s.fileRoot(full)
	if err != nil {
		return nil, err
	}
	return workspace.ReadDir(root, rel)
}
func (s *Server) mkdirManaged(full string) error {
	root, rel, err := s.fileRoot(full)
	if err != nil {
		return err
	}
	return workspace.MkdirAll(root, rel, 0755)
}
func (s *Server) removeManaged(full string) error {
	root, rel, err := s.fileRoot(full)
	if err != nil {
		return err
	}
	return workspace.Remove(root, rel)
}
