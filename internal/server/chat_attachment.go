package server

import (
	"bytes"
	"encoding/hex"
	"mime"
	"net/http"
	"path/filepath"
	"prism/internal/agent"
	"prism/internal/workspace"
	"strings"
	"time"
)

// Downloads only response snapshots, never arbitrary workspace paths.
func (s *Server) handleChatAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	session, ok := s.sessionFor(r, r.URL.Query().Get("session"))
	if !ok {
		http.Error(w, "forbidden", 403)
		return
	}
	id, name := r.URL.Query().Get("id"), r.URL.Query().Get("name")
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\r\n") {
		http.Error(w, "invalid attachment", 400)
		return
	}
	data, err := workspace.ReadFile(s.cfg.WorkspaceDir, agent.AttachmentFilePath(session, id, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(name)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
