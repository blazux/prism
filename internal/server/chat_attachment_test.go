package server

import (
	"net/http/httptest"
	"net/url"
	"prism/internal/agent"
	"prism/internal/memory"
	"prism/internal/workspace"
	"strings"
	"testing"
)

func TestResponseAttachmentDownloadIsolation(t *testing.T) {
	root := t.TempDir()
	s := &Server{cfg: Config{WorkspaceDir: root}}
	id := strings.Repeat("a", 32)
	name := "report & résumé.txt"
	if err := workspace.WriteFile(root, agent.AttachmentFilePath("u7-default", id, name), []byte("private report")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user          int64
		session, name string
		status        int
	}{
		{7, "u7-default", name, 200}, {8, "u7-default", name, 403}, {8, "default", name, 404}, {7, "u7-other", name, 404}, {7, "u7-default", "../secret", 400},
	} {
		q := url.Values{"session": {tc.session}, "id": {id}, "name": {tc.name}}
		r := withUser(httptest.NewRequest("GET", "/api/chat/attachment?"+q.Encode(), nil), &memory.User{ID: tc.user})
		w := httptest.NewRecorder()
		s.handleChatAttachment(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
		if w.Code == 200 && (w.Body.String() != "private report" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("Content-Type") != "application/octet-stream") {
			t.Fatal(w.Result())
		}
	}
}
