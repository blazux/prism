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

	"github.com/jackc/pgx/v5"
	"prism/internal/memory"
)

func TestEmbeddingHotReload(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	schema := fmt.Sprintf("hot_embed_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema+",public")
	parsed.RawQuery = q.Encode()
	ms, err := memory.NewStore(ctx, parsed.String(), bytes.Repeat([]byte{7}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		for _, text := range body.Input {
			if fail.Load() && text == "kept text" {
				http.Error(w, "fixture failure", 400)
				return
			}
		}
		dim := 3
		if body.Model == "four" {
			dim = 4
		}
		data := []any{}
		for i := range body.Input {
			v := make([]float32, dim)
			if body.Model == "three" {
				v[0] = 1
			} else {
				v[dim-1] = 1
			}
			data = append(data, map[string]any{"index": i, "embedding": v})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer upstream.Close()
	s := &Server{memStore: ms, cfg: Config{PostgresURL: parsed.String(), LLMBackend: "openai", OpenAIBaseURL: upstream.URL, Model: "chat"}}
	defer func() {
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
	}()
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for s.ragUpdating.Load() {
			if time.Now().After(deadline) {
				t.Fatal("apply timed out")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	save := func(model string) {
		t.Helper()
		old, e := loadAIProfile(ctx, ms)
		if e != nil {
			t.Fatal(e)
		}
		p := &aiProfile{Provider: "other", BaseURL: upstream.URL, Model: "chat", Embedding: &embeddingProfile{Provider: "other", BaseURL: upstream.URL, Model: model, Reindex: true}}
		if e = s.saveAIProfile(ctx, nil, p, old); e != nil {
			t.Fatal(e)
		}
	}
	vector := func() string {
		t.Helper()
		var v string
		if e := db.QueryRow(ctx, "SELECT embedding::text FROM "+quoted+".rag_chunks WHERE content='kept text'").Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	save("three")
	wait()
	store, _, _, release := s.acquireRAG()
	if store == nil {
		release()
		t.Fatal("save did not enable document search")
	}
	release()
	_, err = db.Exec(ctx, `INSERT INTO `+quoted+`.rag_collections(name,session_id) VALUES('fixture','test'); INSERT INTO `+quoted+`.rag_documents(id,collection,filename,file_hash) VALUES(1,'fixture','file','hash'); INSERT INTO `+quoted+`.rag_chunks(document_id,collection,chunk_index,content,embedding) VALUES(1,'fixture',0,'kept text','[1,0,0]')`)
	if err != nil {
		t.Fatal(err)
	}
	// An operation started with the previous pair must finish before migration.
	_, oldEmbedder, _, release := s.acquireRAG()
	save("other-three")
	next, _, _, done := s.acquireRAG()
	done()
	if next != nil {
		release()
		t.Fatal("new operation accepted during migration")
	}
	time.Sleep(50 * time.Millisecond)
	if vector() != "[1,0,0]" {
		release()
		t.Fatal("migration crossed an active operation")
	}
	release()
	wait()
	if vector() != "[0,0,1]" {
		t.Fatal("same-dimensional model was not reindexed")
	}
	_, current, _, done := s.acquireRAG()
	done()
	if current == oldEmbedder {
		t.Fatal("existing consumers did not receive new embedder")
	}
	fail.Store(true)
	save("four")
	wait()
	if vector() != "[0,0,1]" {
		t.Fatal("failed rebuild lost original vectors")
	}
	status, _ := s.ragInitStatus.Load().(string)
	if !strings.Contains(status, "failed") {
		t.Fatalf("missing failure status: %s", status)
	}
	store, current, _, done = s.acquireRAG()
	done()
	if store == nil || current == nil {
		t.Fatal("previous index unavailable after failure")
	}
	fail.Store(false)
	save("four")
	wait()
	if vector() != "[0,0,0,1]" {
		t.Fatal("retry did not apply dimension change")
	}
	save("")
	wait()
	store, _, _, done = s.acquireRAG()
	done()
	if store != nil {
		t.Fatal("disable requires restart")
	}
	save("four")
	wait()
	store, _, _, done = s.acquireRAG()
	done()
	if store == nil {
		t.Fatal("reenable requires restart")
	}
	// Multiple saves while an old operation runs: only the latest target wins.
	_, _, _, release = s.acquireRAG()
	save("three")
	save("other-three")
	release()
	wait()
	if vector() != "[0,0,1]" {
		t.Fatal("superseded apply overwrote the latest settings")
	}
	if err := s.resetAIProfile(ctx, true); err != nil {
		t.Fatal(err)
	}
	wait()
	store, _, _, done = s.acquireRAG()
	done()
	if store != nil {
		t.Fatal("reset to disabled server defaults did not apply")
	}

}

// A resource that changes embeddings must not publish its progress through
// another resource's status endpoint in the same process.
func TestRAGStatusIsResourceLocal(t *testing.T) {
	a, b := &Server{}, &Server{}
	a.initRAG(context.Background())
	a.ragInitStatus.Store("rebuilding document index: 17 chunks…")
	wa, wb := httptest.NewRecorder(), httptest.NewRecorder()
	a.handleRAGStatus(wa, httptest.NewRequest("GET", "/api/rag/status", nil))
	b.handleRAGStatus(wb, httptest.NewRequest("GET", "/api/rag/status", nil))
	if !strings.Contains(wa.Body.String(), "17 chunks") || strings.Contains(wb.Body.String(), "17 chunks") {
		t.Fatalf("resource status leaked: %s / %s", wa.Body.String(), wb.Body.String())
	}
}
