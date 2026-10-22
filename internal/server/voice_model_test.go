package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"prism/internal/docker"
	"prism/internal/memory"
)

func TestVoiceModelOverridePreservesWebRoutingAndPersona(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	schema := fmt.Sprintf("voice_model_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	ms, err := memory.NewStore(ctx, u.String(), bytes.Repeat([]byte{7}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	var phoneCalls, webCalls atomic.Int32
	backend := func(model string, calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":[{"id":%q}]}`, model)
				return
			}
			var body struct {
				Model string `json:"model"`
				Tools []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tools"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != model {
				t.Error("model routed to wrong source")
				http.Error(w, "wrong model", 400)
				return
			}
			if model == "fast" {
				for _, tool := range body.Tools {
					if tool.Function.Name == "exec_command" {
						t.Error("voice gained execution access")
					}
				}
			}
			calls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Bonjour.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}))
	}
	phone := backend("fast", &phoneCalls)
	defer phone.Close()
	web := backend("heavy", &webCalls)
	defer web.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "heavy", LLMBackend: "openai", OpenAIBaseURL: web.URL, AIDefaultSource: "main", AISources: []aiSource{
		{ID: "main", Provider: "openai", BaseURL: web.URL, Model: "heavy"}, {ID: "phone", Provider: "openai", BaseURL: phone.URL, Model: "fast"},
	}})
	s.memStore = ms
	s.docker = docker.WithExecution(nil)
	ms.SetConfig(ctx, cfgVoicePersonality, "VOICE_PERSONA")
	put := func(body string) int {
		w := httptest.NewRecorder()
		s.handleVoiceConfig(w, withUser(httptest.NewRequest("PUT", "/api/voice", strings.NewReader(body)), serviceUser))
		return w.Code
	}
	if status := put(`{"model":"phone::fast"}`); status != 200 {
		t.Fatalf("model save HTTP %d", status)
	}
	if persona, _, _ := ms.GetConfig(ctx, cfgVoicePersonality); persona != "VOICE_PERSONA" {
		t.Fatal("model-only save erased persona")
	}
	if status := put(`{"personality":"UPDATED_PERSONA"}`); status != 200 {
		t.Fatal(status)
	}
	if s.voiceModel(ctx, "heavy") != "phone::fast" || s.cfg.Model != "heavy" {
		t.Fatal("persona save reset voice model or changed web default")
	}
	if status := put(`{"model":"missing::fast"}`); status != 400 {
		t.Fatal("unknown source accepted")
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleWS(w, withUser(r, serviceUser)) }))
	defer host.Close()
	for _, voice := range []bool{true, false} {
		path := "?session=web-fixture"
		if voice {
			path = "?session=voice-fixture&channel=voice"
		}
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		msg := map[string]any{"type": "chat", "content": "Bonjour"}
		if voice {
			msg["channel"] = "voice"
		}
		if err := c.WriteJSON(msg); err != nil {
			c.Close()
			t.Fatal(err)
		}
		for {
			var ev map[string]any
			if err := c.ReadJSON(&ev); err != nil {
				c.Close()
				t.Fatal(err)
			}
			if ev["type"] == "error" {
				c.Close()
				t.Fatal(ev)
			}
			if ev["type"] == "turn_complete" {
				break
			}
		}
		c.Close()
	}
	if phoneCalls.Load() != 1 || webCalls.Load() != 1 {
		t.Fatalf("phone=%d web=%d", phoneCalls.Load(), webCalls.Load())
	}
	if status := put(`{"model":""}`); status != 200 || s.voiceModel(ctx, "heavy") != "heavy" {
		t.Fatal("default model reset failed")
	}
}
