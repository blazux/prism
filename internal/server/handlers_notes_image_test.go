package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A valid image upload is saved under workspace/data/notes/<scope>/ (served
// by the existing /data/ static mount) and returns its URL.
func TestHandleNoteImage_ValidUpload(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{WorkspaceDir: dir}}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "diagram.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("fake-png-bytes"))
	mw.Close()

	r := httptest.NewRequest("POST", "/api/notes/image", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleNoteImage(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct{ URL string }
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.URL == "" {
		t.Fatal("expected a non-empty url in the response")
	}
	// The returned URL must resolve under the /data/ static mount.
	rel := filepath.FromSlash(resp.URL[len("/data/"):])
	saved := filepath.Join(dir, "data", rel)
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("uploaded file not found on disk at %s: %v", saved, err)
	}
	if string(data) != "fake-png-bytes" {
		t.Fatalf("saved file content mismatch: %q", data)
	}
}

// An unsupported extension is rejected.
func TestHandleNoteImage_RejectsUnsupportedType(t *testing.T) {
	s := &Server{cfg: Config{WorkspaceDir: t.TempDir()}}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "script.exe")
	part.Write([]byte("not an image"))
	mw.Close()

	r := httptest.NewRequest("POST", "/api/notes/image", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleNoteImage(w, r)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 for an unsupported file type", w.Code)
	}
}

// A request with no file field is rejected, not a panic.
func TestHandleNoteImage_MissingFile(t *testing.T) {
	s := &Server{cfg: Config{WorkspaceDir: t.TempDir()}}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.Close()

	r := httptest.NewRequest("POST", "/api/notes/image", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleNoteImage(w, r)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 when no file is provided", w.Code)
	}
}
