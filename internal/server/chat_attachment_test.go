package server

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
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

func TestResponseAttachmentPreview(t *testing.T) {
	root := t.TempDir()
	s := &Server{cfg: Config{WorkspaceDir: root}}
	id := strings.Repeat("b", 32)
	var original bytes.Buffer
	if err := png.Encode(&original, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"photo.png", original.Bytes(), http.StatusOK},
		{"fake.png", []byte("<html><script>alert(1)</script></html>"), http.StatusUnsupportedMediaType},
		{"vector.svg", []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), http.StatusUnsupportedMediaType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := workspace.WriteFile(root, agent.AttachmentFilePath("u7-default", id, tc.name), tc.data); err != nil {
				t.Fatal(err)
			}
			q := url.Values{"session": {"u7-default"}, "id": {id}, "name": {tc.name}, "preview": {"1"}}
			for _, user := range []int64{7, 8} {
				r := withUser(httptest.NewRequest("GET", "/api/chat/attachment?"+q.Encode(), nil), &memory.User{ID: user})
				w := httptest.NewRecorder()
				s.handleChatAttachment(w, r)
				want := tc.status
				if user != 7 {
					want = http.StatusForbidden
				}
				if w.Code != want {
					t.Fatalf("user %d: got %d, want %d", user, w.Code, want)
				}
				if w.Code == http.StatusOK {
					if !bytes.Equal(w.Body.Bytes(), tc.data) ||
						w.Header().Get("Content-Type") != "image/png" ||
						!strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline;") ||
						w.Header().Get("Cache-Control") != "private, no-store" ||
						w.Header().Get("X-Content-Type-Options") != "nosniff" {
						t.Fatalf("invalid preview response: %v", w.Result())
					}
				}
			}
		})
	}
}
