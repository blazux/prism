package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"prism/internal/agent"
	"prism/internal/docker"
	"prism/internal/memory"
	"prism/internal/workspace"
)

type telegramTestTransport func(*http.Request) (*http.Response, error)

func (f telegramTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Route only Telegram requests to a local fixture. No real bot is contacted.
func telegramTestAPI(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	base, _ := url.Parse(host.URL)
	original := http.DefaultTransport
	http.DefaultTransport = telegramTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.telegram.org" {
			r = r.Clone(r.Context())
			u := *r.URL
			u.Scheme, u.Host = base.Scheme, base.Host
			r.URL = &u
		}
		return original.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	return host.URL + "/botfixture"
}

// Exercise download -> saved file -> real headless agent -> provider payload,
// for Telegram photos, uncompressed image documents and text-only models.
func TestTelegramImagesReachModel(t *testing.T) {
	for _, tc := range []struct {
		name, fileName string
		vision         bool
	}{
		{"photo", "photo.jpg", true},
		{"image document", "original.png", true},
		{"text-only model", "photo.jpg", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			png, _ := base64.StdEncoding.DecodeString(chatTestPNG)
			originalTransport := http.DefaultTransport
			telegramTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/botfixture/getFile":
					if r.FormValue("file_id") != "image-id" {
						t.Error("file ID lost")
					}
					fmt.Fprint(w, `{"ok":true,"result":{"file_path":"photos/original.png"}}`)
				case "/file/botfixture/photos/original.png":
					w.Write(png)
				default:
					t.Errorf("unexpected Telegram request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			requests := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Messages []struct {
						Role    string
						Content json.RawMessage
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				requests++
				var user string
				for _, m := range req.Messages {
					if m.Role == "user" {
						user = string(m.Content)
					}
				}
				if !strings.Contains(user, "/workspace/uploads/"+tc.fileName) || !strings.Contains(user, "Describe this") {
					t.Errorf("image path or caption missing: %s", user)
				}
				if strings.Contains(user, chatTestPNG) != tc.vision {
					t.Errorf("vision enabled=%t, payload=%s", tc.vision, user)
				}
				if !tc.vision && !strings.Contains(user, "cannot inspect images") {
					t.Error("text-only model lost the image limitation notice")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"A picture.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer backend.Close()
			s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, ChatVision: tc.vision})
			s.docker = docker.WithExecution(nil)
			text, images, err := s.attachTelegramFiles(t.Context(), "fixture", []string{"image-id"}, []string{tc.fileName}, "Describe this")
			if err != nil || len(images) != 1 {
				t.Fatalf("incoming photo: images=%d err=%v", len(images), err)
			}
			// Provider clients clone the standard *http.Transport. Telegram has
			// finished downloading, so remove the fixture wrapper before creating it.
			http.DefaultTransport = originalTransport
			got, err := workspace.ReadFile(s.cfg.WorkspaceDir, "uploads/"+tc.fileName)
			if err != nil || !bytes.Equal(got, png) {
				t.Fatal("original image unavailable to tools", err)
			}
			resp, err := s.runHeadlessChatImagesTap(t.Context(), "telegram", text, "", images, CallerContext{}, nil, agent.Limits{})
			if err != nil || resp != "A picture." || requests != 1 {
				t.Fatalf("headless vision: response=%q requests=%d err=%v", resp, requests, err)
			}
		})
	}
}

func TestTelegramAgentAttachmentsAreUploaded(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	const report = "report & résumé.txt"
	png, _ := base64.StdEncoding.DecodeString(chatTestPNG)
	root := t.TempDir()
	if err := workspace.WriteFile(root, report, []byte("original report")); err != nil {
		t.Fatal(err)
	}
	if err := workspace.WriteFile(root, "image.png", png); err != nil {
		t.Fatal(err)
	}
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			tools := []map[string]any{}
			for i, path := range []string{report, "image.png"} {
				args, _ := json.Marshal(map[string]string{"path": path})
				tools = append(tools, map[string]any{"index": i, "id": fmt.Sprintf("call%d", i), "type": "function", "function": map[string]string{"name": "add_attachment", "arguments": string(args)}})
			}
			data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": tools}, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Here are your files.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	s := New(Config{WorkspaceDir: root, PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL})
	s.docker = docker.WithExecution(nil)
	var files responseAttachments
	text, err := s.runHeadlessChatImagesTap(t.Context(), "u7-telegram", "Attach the report and image", "", nil, CallerContext{}, files.tap, agent.Limits{})
	if err != nil || len(files) != 2 || requests != 2 {
		t.Fatalf("agent response: %q files=%v requests=%d err=%v", text, files, requests, err)
	}
	// Delivery must use the immutable snapshot, even if the source was changed.
	workspace.WriteFile(root, report, []byte("changed after attachment"))
	var calls []string
	api := telegramTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			if r.FormValue("chat_id") != "42" || r.FormValue("text") != text {
				t.Error("wrong recipient or response text")
			}
		} else {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			field, name, want := "document", report, []byte("original report")
			if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
				field, name, want = "photo", "image.png", png
			}
			f, header, err := r.FormFile(field)
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			got, _ := io.ReadAll(f)
			if r.FormValue("chat_id") != "42" || header.Filename != name || !bytes.Equal(got, want) {
				t.Errorf("incorrect uploaded file: name=%q bytes=%q", header.Filename, got)
			}
		}
		fmt.Fprint(w, `{"ok":true,"result":{}}`)
	})
	if err := s.tgSendResponse(t.Context(), api, 42, "u7-telegram", text, files); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "/botfixture/sendMessage,/botfixture/sendDocument,/botfixture/sendPhoto" {
		t.Fatal("attachments missing or sent twice", calls)
	}
}

func telegramTestAttachment(t *testing.T, s *Server, name string, data []byte) responseAttachment {
	t.Helper()
	const session = "u7-telegram"
	id := strings.Repeat("a", 32)
	if err := workspace.WriteFile(s.cfg.WorkspaceDir, agent.AttachmentFilePath(session, id, name), data); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"session": {session}, "id": {id}, "name": {name}}
	return responseAttachment{Name: name, URL: "/api/chat/attachment?" + q.Encode()}
}

func TestTelegramAttachmentRejectsForeignAndInvalidReferences(t *testing.T) {
	s := &Server{cfg: Config{WorkspaceDir: t.TempDir()}}
	file := telegramTestAttachment(t, s, "file.txt", []byte("private"))
	calls := 0
	api := telegramTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"ok":true}`)
	})
	for _, rawURL := range []string{
		strings.Replace(file.URL, "u7-telegram", "u8-telegram", 1),
		"https://example.test" + file.URL,
		"//example.test" + file.URL,
		"/api/chat/attachment?session=u7-telegram&id=bad&name=file.txt",
		strings.Replace(file.URL, "file.txt", "..%2Fsecret", 1),
		strings.Replace(file.URL, "file.txt", "%00", 1),
		"/workspace/file.txt",
	} {
		if err := s.tgSendAttachment(t.Context(), api, 42, "u7-telegram", responseAttachment{URL: rawURL}); err == nil {
			t.Errorf("invalid reference accepted: %s", rawURL)
		}
	}
	path, _, _ := responseAttachmentPath("u7-telegram", file.URL)
	if err := os.Remove(filepath.Join(s.cfg.WorkspaceDir, path)); err != nil {
		t.Fatal(err)
	}
	if err := s.tgSendAttachment(t.Context(), api, 42, "u7-telegram", file); err == nil {
		t.Fatal("missing snapshot accepted")
	}
	if calls != 0 {
		t.Fatal("invalid attachments reached Telegram")
	}
}

func TestTelegramAttachmentPhotoFallbackAndAPIFailure(t *testing.T) {
	s := &Server{cfg: Config{WorkspaceDir: t.TempDir()}}
	png, _ := base64.StdEncoding.DecodeString(chatTestPNG)
	file := telegramTestAttachment(t, s, "image.png", png)
	var calls []string
	api := telegramTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"PHOTO_INVALID_DIMENSIONS"}`)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	})
	if err := s.tgSendResponse(t.Context(), api, 42, "u7-telegram", "", responseAttachments{file}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "/botfixture/sendPhoto,/botfixture/sendDocument" {
		t.Fatal("photo fallback lost, or attachment-only reply produced extra text", calls)
	}
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error_code":403}`)
	}))
	defer blocked.Close()
	if err := s.tgSendResponse(t.Context(), blocked.URL, 42, "u7-telegram", "", responseAttachments{file}); err == nil {
		t.Fatal("Telegram API rejection silently ignored")
	}
}

func TestTelegramUserRoutingAndUnauthorizedFiles(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms, cfg: Config{WorkspaceDir: t.TempDir(), MultiUser: true}}
	s.initChannels()
	type delivery struct{ path, chat string }
	var deliveries []delivery
	api := telegramTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendDocument") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
		} else if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("unauthorized attachment caused a download: %s", r.URL.Path)
		}
		deliveries = append(deliveries, delivery{r.URL.Path, r.FormValue("chat_id")})
		fmt.Fprint(w, `{"ok":true}`)
	})
	for i := 1; i <= 2; i++ {
		u, err := ms.CreateUser(t.Context(), fmt.Sprintf("telegram%d@example.test", i), "unused", "fixture", memory.RoleMember, memory.StatusApproved)
		if err != nil {
			t.Fatal(err)
		}
		scope := s.tgScope(u.ID)
		token, chat := fmt.Sprintf("fixture%d", i), strconv.Itoa(100+i)
		if err := scope.SetSecret(t.Context(), tgTokenSecret, token); err != nil {
			t.Fatal(err)
		}
		if err := scope.SetConfig(t.Context(), tgAllowedChatKey, chat); err != nil {
			t.Fatal(err)
		}
		session := fmt.Sprintf("u%d-telegram", u.ID)
		id, name := strings.Repeat("a", 32), "report.txt"
		if err := workspace.WriteFile(s.cfg.WorkspaceDir, agent.AttachmentFilePath(session, id, name), []byte(chat)); err != nil {
			t.Fatal(err)
		}
		q := url.Values{"session": {session}, "id": {id}, "name": {name}}
		files := responseAttachments{{Name: name, URL: "/api/chat/attachment?" + q.Encode()}}
		if err := s.deliverResponseToChannel(t.Context(), "telegram", session, "", files); err != nil {
			t.Fatal(err)
		}
		if got := deliveries[len(deliveries)-1]; got != (delivery{"/bot" + token + "/sendDocument", chat}) {
			t.Fatalf("wrong user received an attachment: %+v", got)
		}
		// Unauthorized senders must be rejected before getFile or persistence.
		s.handleTelegramMessage(t.Context(), api, token, u.ID, 999, "Read this", []string{"not-allowed"}, []string{"photo.jpg"})
		if err := scope.SetConfig(t.Context(), tgAllowedChatKey, ""); err != nil {
			t.Fatal(err)
		}
		s.handleTelegramMessage(t.Context(), api, token, u.ID, 999, "Read this", []string{"not-linked"}, []string{"photo.jpg"})
	}
	if _, err := os.Stat(filepath.Join(s.cfg.WorkspaceDir, "uploads")); !os.IsNotExist(err) {
		t.Fatal("files from an unauthorized/unlinked sender were saved")
	}
}
