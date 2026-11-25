package ollama_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"prism/internal/anthropic"
	"prism/internal/httpguard"
	"prism/internal/ollama"
	"prism/internal/openai"
)

const redirectFixtureKey = "sk-ant-api03-synthetic-redirect-fixture"
const redirectFixturePrompt = "Synthetic private conversation fixture"

type redirectBackendFixture struct {
	name     string
	new      func(string) ollama.Backend
	key      string
	response string
}

func redirectBackends() []redirectBackendFixture {
	return []redirectBackendFixture{
		{name: "anthropic", new: func(base string) ollama.Backend { return anthropic.NewClient(base, redirectFixtureKey, "fixture") }, key: "x-api-key",
			response: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ready\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{name: "openai", new: func(base string) ollama.Backend { return openai.NewClient(base, redirectFixtureKey) }, key: "Authorization",
			response: "data: {\"choices\":[{\"delta\":{\"content\":\"ready\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{name: "ollama", new: func(base string) ollama.Backend { return ollama.NewClient(base) },
			response: "{\"message\":{\"role\":\"assistant\",\"content\":\"ready\"},\"done\":true,\"done_reason\":\"stop\"}\n"},
	}
}

func runRedirectFixtureChat(t *testing.T, backend ollama.Backend) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	out := make(chan ollama.StreamEvent, 32)
	backend.Chat(ctx, ollama.ChatRequest{Model: "fixture", Messages: []ollama.Message{{Role: "user", Content: redirectFixturePrompt}}}, out)
	close(out)
	var content strings.Builder
	var streamErr error
	for event := range out {
		content.WriteString(event.Content)
		if event.Err != nil {
			streamErr = event.Err
		}
	}
	return content.String(), streamErr
}

func TestModelChatRejectsCrossOriginRedirects(t *testing.T) {
	for _, backend := range redirectBackends() {
		for _, status := range []int{302, 307, 308} {
			t.Run(fmt.Sprintf("%s/%d", backend.name, status), func(t *testing.T) {
				var sourceCalls, destinationCalls atomic.Int32
				var forwardedCredential, forwardedPrompt atomic.Bool
				destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					destinationCalls.Add(1)
					body, _ := io.ReadAll(r.Body)
					forwardedPrompt.Store(strings.Contains(string(body), redirectFixturePrompt))
					forwardedCredential.Store(strings.Contains(r.Header.Get(backend.key), redirectFixtureKey))
					fmt.Fprint(w, backend.response)
				}))
				defer destination.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sourceCalls.Add(1)
					w.Header().Set("Location", destination.URL+"/collect?credential="+redirectFixtureKey)
					w.WriteHeader(status)
				}))
				defer source.Close()
				_, err := runRedirectFixtureChat(t, backend.new(source.URL))
				if got := destinationCalls.Load(); got != 0 {
					t.Errorf("redirect destination was contacted %d times (credential forwarded: %t, prompt forwarded: %t)", got, forwardedCredential.Load(), forwardedPrompt.Load())
				}
				if got := sourceCalls.Load(); got != 1 {
					t.Errorf("permanent redirect refusal repeated the original request: %d calls", got)
				}
				if !errors.Is(err, httpguard.ErrRedirect) || !strings.Contains(err.Error(), "redirect blocked") {
					t.Fatal("Chat did not report a clear redirect policy error")
				}
				if strings.Contains(err.Error(), redirectFixtureKey) || strings.Contains(err.Error(), redirectFixturePrompt) {
					t.Fatal("redirect error exposed a credential or request body")
				}
			})
		}
	}
}

func TestModelChatStopsRedirectLoopsWithoutRetrying(t *testing.T) {
	for _, backend := range redirectBackends() {
		t.Run(backend.name, func(t *testing.T) {
			var calls atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/loop?credential="+redirectFixtureKey)
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer source.Close()
			_, err := runRedirectFixtureChat(t, backend.new(source.URL))
			if !errors.Is(err, httpguard.ErrRedirect) || strings.Contains(err.Error(), redirectFixtureKey) {
				t.Fatal("redirect loop did not return a private permanent policy error")
			}
			if calls.Load() != 10 {
				t.Fatalf("redirect loop did not retain the normal limit without retries: %d calls", calls.Load())
			}
		})
	}
}

func TestModelChatKeepsSameOriginRedirects(t *testing.T) {
	for _, backend := range redirectBackends() {
		for _, status := range []int{302, 307, 308} {
			t.Run(fmt.Sprintf("%s/%d", backend.name, status), func(t *testing.T) {
				var canonicalCalls atomic.Int32
				var source *httptest.Server
				source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/canonical" {
						w.Header().Set("Location", source.URL+"/canonical")
						w.WriteHeader(status)
						return
					}
					canonicalCalls.Add(1)
					if backend.key != "" && !strings.Contains(r.Header.Get(backend.key), redirectFixtureKey) {
						t.Error("same-origin redirect lost its API credential")
					}
					body, _ := io.ReadAll(r.Body)
					if status != 302 && (r.Method != http.MethodPost || !strings.Contains(string(body), redirectFixturePrompt)) {
						t.Error("same-origin 307/308 redirect changed the method or body")
					}
					fmt.Fprint(w, backend.response)
				}))
				defer source.Close()
				content, err := runRedirectFixtureChat(t, backend.new(source.URL))
				if err != nil || content != "ready" || canonicalCalls.Load() != 1 {
					t.Fatal("same-origin canonical path did not return the model reply")
				}
			})
		}
	}
}
