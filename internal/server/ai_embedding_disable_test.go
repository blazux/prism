package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDisabledDedicatedEmbeddingCredentialIntent(t *testing.T) {
	const endpoint = "https://embedding.fixture.invalid/v1"
	for _, tc := range []struct {
		name     string
		previous embeddingProfile
		draft    embeddingProfile
		wantKey  string
	}{
		{"keep saved key", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "other", BaseURL: endpoint}, "fixture-saved-key"},
		{"normalized connection", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "openai", BaseURL: " " + endpoint + "/ "}, "fixture-saved-key"},
		{"remove saved key", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "other", BaseURL: endpoint, ClearKey: true}, ""},
		{"replace saved key", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-new-key"}, "fixture-new-key"},
		{"removal wins", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-new-key", ClearKey: true}, ""},
		{"different endpoint", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "other", BaseURL: "https://another.fixture.invalid/v1"}, ""},
		{"different provider", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "ollama", BaseURL: endpoint}, ""},
		{"incomplete endpoint stays disabled", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{Provider: "other"}, ""},
		{"do not copy conversation key", embeddingProfile{UseSameProvider: true, Provider: "other", BaseURL: endpoint, APIKey: "fixture-chat-key"}, embeddingProfile{Provider: "other", BaseURL: endpoint}, ""},
		{"do not copy referenced source key", embeddingProfile{SourceID: "chat", Provider: "other", BaseURL: endpoint, APIKey: "fixture-chat-key"}, embeddingProfile{Provider: "other", BaseURL: endpoint}, ""},
		{"new shared connection", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{UseSameProvider: true, Provider: "other", BaseURL: endpoint}, ""},
		{"new source reference", embeddingProfile{Provider: "other", BaseURL: endpoint, APIKey: "fixture-saved-key"}, embeddingProfile{SourceID: "chat", Provider: "other", BaseURL: endpoint}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous, draft := tc.previous, tc.draft
			previous.Model = "fixture-embedding"
			draft.Reindex, draft.ReindexFor = true, "previous-consent"
			old := &aiProfile{Provider: "other", BaseURL: endpoint, Model: "chat", APIKey: "fixture-chat-key", Embedding: &previous}
			// A conversation/source key may have been explicitly removed in the
			// same save. Disabling search must not copy it into dedicated storage.
			p := &aiProfile{Provider: "other", BaseURL: endpoint, Model: "chat", Embedding: &draft}
			if err := (&Server{}).validateEmbeddingSave(t.Context(), p, old); err != nil {
				t.Fatalf("disable failed: %v", err)
			}
			if p.Embedding.APIKey != tc.wantKey {
				t.Fatal("disabled embedding did not preserve the requested credential state")
			}
			if p.Embedding.Model != "" || p.Embedding.Reindex || p.Embedding.ReindexFor != "" {
				t.Fatal("disabled embedding retained a model or reindex consent")
			}
		})
	}
}

func TestDisabledEmbeddingNeedsNoConnectionProbeOrReindexPermission(t *testing.T) {
	old := &aiProfile{Provider: "anthropic", BaseURL: "https://api.anthropic.com", Model: "chat", Embedding: &embeddingProfile{Provider: "ollama", BaseURL: "http://unused.invalid", Model: "old-embed"}}
	for _, e := range []*embeddingProfile{
		{Provider: "ollama", BaseURL: "", Model: "", Reindex: true, ReindexFor: "old-permission"},
		{SourceID: "removed-source", Provider: "other", BaseURL: "http://unused.invalid", Model: ""},
		{UseSameProvider: true, Model: ""},
		{Provider: "other", BaseURL: "http://unused.invalid", Model: "", APIKey: "synthetic-private-key", ClearKey: true},
	} {
		p := &aiProfile{Provider: "anthropic", BaseURL: "https://api.anthropic.com", Model: "chat", Embedding: e}
		if err := (&Server{}).validateEmbeddingSave(t.Context(), p, old); err != nil {
			t.Fatalf("disabled embedding required its hidden connection or consent: %v", err)
		}
		if p.Embedding.Reindex || p.Embedding.ReindexFor != "" || p.Embedding.ClearKey && p.Embedding.APIKey != "" {
			t.Fatal("disable retained reindex permission or a removed dedicated key")
		}
	}
}

func TestDedicatedEmbeddingDisablePersistsCredentialIntent(t *testing.T) {
	for _, tc := range []struct {
		name, entered, wantKey string
		clear                  bool
	}{
		{name: "keep", wantKey: "fixture-saved-key"},
		{name: "replace", entered: "fixture-new-key", wantKey: "fixture-new-key"},
		{name: "remove", clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := securityStoreWithDSN(t)
			var probes atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probes.Add(1)
				if r.Method != "POST" || r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer "+tc.wantKey || tc.wantKey == "" {
					t.Error("embedding reactivation did not use the requested dedicated credential")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0,0]}]}`))
			}))
			defer upstream.Close()
			endpoint := upstream.URL + "/v1"
			old := &aiProfile{Provider: "ollama", BaseURL: "http://chat.fixture.invalid", Model: "chat", DefaultSource: "chat",
				Sources:   []aiSource{{ID: "chat", Name: "Chat", Provider: "ollama", BaseURL: "http://chat.fixture.invalid", Model: "chat"}},
				Embedding: &embeddingProfile{Provider: "other", BaseURL: endpoint, Model: "embed", APIKey: "fixture-saved-key"}}
			raw, err := json.Marshal(old)
			if err != nil || store.SetSecret(t.Context(), aiProfileSecret, string(raw)) != nil {
				t.Fatal("cannot store synthetic configuration")
			}
			s := &Server{memStore: store, cfg: Config{LLMBackend: "ollama", OllamaURL: "http://chat.fixture.invalid", Model: "chat"}}
			body, _ := json.Marshal(map[string]any{
				"action": "save", "defaultSource": "chat", "sources": old.Sources,
				"embedding": map[string]any{"provider": "other", "baseURL": endpoint, "model": "", "apiKey": tc.entered, "clearKey": tc.clear},
			})
			w := httptest.NewRecorder()
			s.handleAIProfile(w, withUser(httptest.NewRequest("POST", "/api/ai/config", bytes.NewReader(body)), serviceUser))
			if w.Code != http.StatusOK {
				t.Fatalf("disable failed with status %d", w.Code)
			}
			deadline := time.Now().Add(5 * time.Second)
			for s.ragUpdating.Load() {
				if time.Now().After(deadline) {
					t.Fatal("disabled configuration did not finish applying")
				}
				time.Sleep(time.Millisecond)
			}
			if probes.Load() != 0 {
				t.Fatal("disabling search contacted the embedding provider")
			}
			saved, err := loadAIProfile(t.Context(), store)
			if err != nil || saved == nil || saved.Embedding == nil || saved.Embedding.APIKey != tc.wantKey || saved.Embedding.Model != "" || saved.Embedding.ClearKey {
				t.Fatal("saved credential state did not survive reload")
			}
			w = httptest.NewRecorder()
			s.handleAIProfile(w, withUser(httptest.NewRequest("GET", "/api/ai/config", nil), serviceUser))
			var view struct {
				Embedding struct {
					KeyConfigured bool `json:"keyConfigured"`
				} `json:"embedding"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Embedding.KeyConfigured != (tc.wantKey != "") ||
				strings.Contains(w.Body.String(), "fixture-saved-key") || strings.Contains(w.Body.String(), "fixture-new-key") {
				t.Fatal("public configuration exposed a credential or reported the wrong saved state")
			}
			// Re-enable with the usual blank saved-key field. An explicit removal
			// must stay removed; retained/replaced keys must work for the probe.
			reenabled := *saved
			reenabled.Embedding = &embeddingProfile{Provider: "other", BaseURL: endpoint, Model: "embed", Reindex: true}
			if tc.wantKey == "" {
				if err := s.prepareEmbedding(&reenabled, saved); err != nil || reenabled.Embedding.APIKey != "" {
					t.Fatal("reactivation resurrected a removed credential")
				}
			} else if err := s.validateEmbeddingSave(t.Context(), &reenabled, saved); err != nil || reenabled.Embedding.APIKey != tc.wantKey || probes.Load() != 1 {
				t.Fatal("reactivation could not use the saved dedicated credential")
			}
		})
	}
}

func TestEmbeddingDisableAndCredentialRemovalKeepIndexAndStopRuntime(t *testing.T) {
	for _, removeFirst := range []bool{false, true} {
		name := "disable-only"
		if removeFirst {
			name = "remove-requires-disable"
		}
		t.Run(name, func(t *testing.T) {
			store, dsn := securityStoreWithDSN(t)
			ctx := t.Context()
			var requests atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/v1/embeddings" || r.Method != "POST" {
					t.Errorf("unexpected non-embedding operation: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer fixture-private-key" {
					t.Error("disconnected embedding request reached provider")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0,0]}]}`))
			}))
			defer upstream.Close()
			p := &aiProfile{Provider: "other", BaseURL: upstream.URL + "/v1", APIKey: "fixture-private-key", Model: "chat",
				DefaultSource: "fixture", Sources: []aiSource{{ID: "fixture", Name: "Fixture", Provider: "other", BaseURL: upstream.URL + "/v1", APIKey: "fixture-private-key", Model: "chat"}},
				Embedding: &embeddingProfile{UseSameProvider: true, Model: "embed"}}
			raw, _ := json.Marshal(p)
			if err := store.SetSecret(ctx, aiProfileSecret, string(raw)); err != nil {
				t.Fatal(err)
			}
			s := &Server{memStore: store, cfg: Config{PostgresURL: dsn, LLMBackend: "openai", OpenAIBaseURL: upstream.URL + "/v1", OpenAIAPIKey: "fixture-private-key", Model: "chat", EmbedModel: "embed"}}
			t.Cleanup(func() {
				s.mu.Lock()
				if s.ragCancel != nil {
					s.ragCancel()
				}
				s.mu.Unlock()
				s.ragApplyMu.Lock()
				defer s.ragApplyMu.Unlock()
				if s.ragStore != nil {
					s.ragStore.Close()
				}
			})
			// Initialize synchronously, without starting help ingestion, so later
			// provider calls can only come from the configuration under test.
			if err := s.applyRAGProfile(ctx); err != nil {
				t.Fatal(err)
			}
			initialRequests := requests.Load()
			if initialRequests == 0 || s.ragStore == nil {
				t.Fatal("fixture did not establish active document search")
			}
			db, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close(context.Background())
			_, err = db.Exec(ctx, `INSERT INTO rag_collections(name,session_id) VALUES('fixture','test'); INSERT INTO rag_documents(id,collection,filename,file_hash) VALUES(42,'fixture','file','hash'); INSERT INTO rag_chunks(document_id,collection,chunk_index,content,embedding) VALUES(42,'fixture',0,'preserved document text','[1,0,0]')`)
			if err != nil {
				t.Fatal(err)
			}
			save := func(model string, remove bool) *httptest.ResponseRecorder {
				t.Helper()
				body := map[string]any{"action": "save", "defaultSource": "fixture", "sources": []map[string]any{{"id": "fixture", "name": "Fixture", "provider": "other", "baseURL": upstream.URL + "/v1", "model": "chat", "apiKey": "", "clearKey": remove}}, "embedding": map[string]any{"useSameProvider": true, "model": model}}
				encoded, _ := json.Marshal(body)
				r := withUser(httptest.NewRequest("POST", "/api/ai/config", bytes.NewReader(encoded)), serviceUser)
				w := httptest.NewRecorder()
				s.handleAIProfile(w, r)
				return w
			}
			if removeFirst {
				w := save("embed", true)
				if w.Code != 409 || !strings.Contains(w.Body.String(), "Disable document search") {
					t.Fatalf("key removal claimed success while old credential could stay active: %d %s", w.Code, w.Body.String())
				}
				kept, err := loadAIProfile(ctx, store)
				if err != nil || kept.APIKey != "fixture-private-key" || requests.Load() != initialRequests {
					t.Fatal("failed credential removal changed the saved key or probed the provider")
				}
			}
			if w := save("", removeFirst); w.Code != 200 {
				t.Fatalf("disable without reindex permission failed: %d %s", w.Code, w.Body.String())
			}
			deadline := time.Now().Add(5 * time.Second)
			for s.ragUpdating.Load() {
				if time.Now().After(deadline) {
					t.Fatal("document search did not stop")
				}
				time.Sleep(time.Millisecond)
			}
			active, embedder, _, release := s.acquireRAG()
			release()
			status, _ := s.ragInitStatus.Load().(string)
			if active != nil || embedder != nil || !strings.HasPrefix(status, "disabled:") || requests.Load() != initialRequests {
				t.Fatalf("disabled runtime still has provider access: status=%s calls=%d", status, requests.Load())
			}
			var content, vector string
			if err := db.QueryRow(ctx, "SELECT content,embedding::text FROM rag_chunks WHERE document_id=42").Scan(&content, &vector); err != nil || content != "preserved document text" || vector != "[1,0,0]" {
				t.Fatal("disabling search changed or removed document text/vectors", err)
			}
			saved, err := loadAIProfile(ctx, store)
			if err != nil || saved.Embedding.Model != "" || saved.Embedding.Reindex || saved.Embedding.ReindexFor != "" {
				t.Fatal("disable did not persist without reindex permission", err)
			}
			if removeFirst && (saved.APIKey != "" || saved.Sources[0].APIKey != "") {
				t.Fatal("combined disable/removal retained the deleted source key")
			}
		})
	}
}

func TestEmbeddingIdentityChangeStillRequiresReindexPermission(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected operation: %s", r.URL.Path)
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0,0]}]}`))
	}))
	defer server.Close()
	old := &aiProfile{Provider: "other", BaseURL: server.URL, Model: "chat", Embedding: &embeddingProfile{Provider: "other", BaseURL: server.URL, Model: "old-model"}}
	p := &aiProfile{Provider: "other", BaseURL: server.URL, Model: "chat", Embedding: &embeddingProfile{Provider: "other", BaseURL: server.URL, Model: "new-model"}}
	if err := (&Server{}).validateEmbeddingSave(t.Context(), p, old); err == nil || !strings.Contains(err.Error(), "confirm rebuilding") {
		t.Fatal("a real embedding identity change bypassed consent", err)
	}
	p.Embedding.Reindex = true
	if err := (&Server{}).validateEmbeddingSave(t.Context(), p, old); err != nil || p.Embedding.ReindexFor != embeddingIdentity(p.effectiveEmbedding()) {
		t.Fatal("explicit reindex permission was not attached to the new identity", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("identity checks did not use the local dimension fixture: %d", requests.Load())
	}
}
