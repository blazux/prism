package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/agent"
)

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	tree := s.buildFileTree(s.cfg.WorkspaceDir)
	json.NewEncoder(w).Encode(map[string]interface{}{"files": tree})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", 400)
		return
	}
	cleanPath := filepath.Clean(agent.NormalizeWorkspacePath(path))
	if strings.HasPrefix(cleanPath, "..") {
		http.Error(w, "invalid path", 400)
		return
	}
	fullPath := filepath.Join(s.cfg.WorkspaceDir, cleanPath)

	switch r.Method {
	case "GET":
		data, err := os.ReadFile(fullPath)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	case "POST", "PUT":
		var body struct{ Content string }
		json.NewDecoder(r.Body).Decode(&body)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		if err := os.WriteFile(fullPath, []byte(body.Content), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	}
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	// Same capability as /api/terminal by another door — one shell command in the
	// tools container. Gating one without the other would be theatre.
	if !s.requireAdminUser(w, r) {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	var body struct{ Command string }
	json.NewDecoder(r.Body).Decode(&body)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	out, err := s.docker.Exec(ctx, "cd /workspace && "+body.Command, 30*time.Second)
	resp := map[string]interface{}{"output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	json.NewEncoder(w).Encode(resp)
}

// safeWorkspacePath joins workspaceDir with untrusted user-supplied path and
// returns an error if the result escapes the workspace directory.
func safeWorkspacePath(workspaceDir, untrustedPath string) (string, error) {
	full := filepath.Join(workspaceDir, filepath.Clean(untrustedPath))
	rel, err := filepath.Rel(workspaceDir, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid path")
	}
	return full, nil
}

type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"isDir"`
	Children []FileNode `json:"children,omitempty"`
	Size     int64      `json:"size,omitempty"`
}

func (s *Server) buildFileTree(root string) []FileNode {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var nodes []FileNode
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, _ := entry.Info()
		node := FileNode{
			Name:  entry.Name(),
			Path:  entry.Name(),
			IsDir: entry.IsDir(),
		}
		if !entry.IsDir() && info != nil {
			node.Size = info.Size()
		}
		if entry.IsDir() {
			node.Children = s.buildSubTree(filepath.Join(root, entry.Name()), entry.Name(), 0)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func (s *Server) buildSubTree(dir, prefix string, depth int) []FileNode {
	if depth > 4 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var nodes []FileNode
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, _ := entry.Info()
		relPath := prefix + "/" + entry.Name()
		node := FileNode{
			Name:  entry.Name(),
			Path:  relPath,
			IsDir: entry.IsDir(),
		}
		if !entry.IsDir() && info != nil {
			node.Size = info.Size()
		}
		if entry.IsDir() {
			node.Children = s.buildSubTree(filepath.Join(dir, entry.Name()), relPath, depth+1)
		}
		nodes = append(nodes, node)
	}
	return nodes
}
