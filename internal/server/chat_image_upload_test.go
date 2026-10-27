package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"prism/internal/docker"
)

const chatTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="

func TestChatImagesSaveOriginalBytesWithoutOverwriting(t *testing.T) {
	s := &Server{cfg: Config{WorkspaceDir: t.TempDir()}}
	data, _ := base64.StdEncoding.DecodeString(chatTestPNG)
	first, err := s.saveChatImages([]string{chatTestPNG, chatTestPNG}, []string{"../photo.png", "photo.png"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.saveChatImages([]string{chatTestPNG}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Path == first[1].Path || first[0].Name != "photo.png" || legacy[0].Path != "uploads/image-1.png" {
		t.Fatalf("names not preserved/sanitized/unique: %+v %+v", first, legacy)
	}
	for _, a := range append(first, legacy...) {
		got, err := s.readManagedFile(filepath.Join(s.cfg.WorkspaceDir, a.Path))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("original image not saved at %q: %v", a.Path, err)
		}
		preamble := attachmentPreamble(a)
		if !strings.Contains(preamble, "/workspace/"+a.Path) || !strings.Contains(preamble, "no download is needed") {
			t.Fatalf("agent cannot locate image: %s", preamble)
		}
	}
}

func TestChatImagesRejectInvalidOrUnsavedUploads(t *testing.T) {
	s := &Server{cfg: Config{WorkspaceDir: t.TempDir()}}
	for _, encoded := range []string{"", "not-base64!", strings.Repeat("A", base64.StdEncoding.EncodedLen(attachmentMaxBytes)+4)} {
		if _, err := s.saveChatImages([]string{encoded}, nil); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if err := os.WriteFile(filepath.Join(s.cfg.WorkspaceDir, "uploads"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.saveChatImages([]string{chatTestPNG}, nil); err == nil {
		t.Fatal("failed persistence must not silently send an inaccessible image")
	}
}

// Exercise the real WebSocket -> agent -> provider path: saving the image must
// retain vision input and give even a text-only model the local file path.
func TestChatImageReachesModelAsFileAndVision(t *testing.T) {
	for _, vision := range []bool{true, false} {
		t.Run(fmt.Sprintf("vision=%t", vision), func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			requests := make(chan []byte, 8)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					http.NotFound(w, r)
					return
				}
				var req json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				requests <- req
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Saved.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer backend.Close()
			s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, ChatVision: vision, AgentContainer: "fixture"})
			s.docker = docker.WithExecution(nil)
			host := httptest.NewServer(http.HandlerFunc(s.handleWS))
			defer host.Close()
			c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"?session=image-test", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := c.WriteJSON(WSMessage{Type: "chat", Content: "Process this image", Images: []string{chatTestPNG}, ImageNames: []string{"photo.png"}}); err != nil {
				t.Fatal(err)
			}
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			for {
				var event struct {
					Type, Content string
				}
				if err := c.ReadJSON(&event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "error" {
					t.Fatal(event.Content)
				}
				if event.Type == "turn_complete" {
					break
				}
			}
			select {
			case raw := <-requests:
				var req struct {
					Messages []struct {
						Role    string
						Content json.RawMessage
					}
				}
				if err := json.Unmarshal(raw, &req); err != nil {
					t.Fatal(err)
				}
				var user string
				for _, m := range req.Messages {
					if m.Role == "user" {
						user = string(m.Content)
					}
				}
				if !strings.Contains(user, "/workspace/uploads/photo.png") || !strings.Contains(user, "Process this image") {
					t.Fatalf("model missing image file path or question: %s", user)
				}
				if strings.Contains(user, chatTestPNG) != vision {
					t.Fatalf("vision payload changed (enabled=%t): %s", vision, user)
				}
				if !vision && !strings.Contains(user, "cannot inspect images") {
					t.Fatal("text-only model must retain its vision limitation notice")
				}
			default:
				t.Fatal("provider not called")
			}
			got, err := s.readManagedFile(filepath.Join(s.cfg.WorkspaceDir, "uploads/photo.png"))
			want, _ := base64.StdEncoding.DecodeString(chatTestPNG)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("model path does not point to the original image", err)
			}
		})
	}
}
