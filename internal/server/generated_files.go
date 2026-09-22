package server

import (
	"io"
	"net/http"
	"path/filepath"
	"prism/internal/workspace"
	"strings"
)

func (s *Server) generatedFS(dir string) http.FileSystem {
	rel, err := filepath.Rel(s.cfg.WorkspaceDir, dir)
	if s.cfg.WorkspaceDir != "" && err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		return workspace.SubFS{Root: s.cfg.WorkspaceDir, Prefix: rel}
	}
	return workspace.FS(dir)
}
func (s *Server) readGeneratedFile(dir, name string) ([]byte, error) {
	f, err := s.generatedFS(dir).Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
