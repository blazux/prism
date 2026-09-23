package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestReindexTransactionalModelChange(t *testing.T) {
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
	schema := fmt.Sprintf("reindex_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema+",public")
	q.Set("pool_min_conns", "0")
	q.Set("pool_max_conns", "2")
	q.Set("pool_max_conn_idle_time", "30s")
	parsed.RawQuery = q.Encode()
	scoped := parsed.String()
	store, err := NewStore(ctx, scoped, 3)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err = db.Exec(ctx, `INSERT INTO `+quoted+`.rag_collections(name,session_id) VALUES('fixture','test');
 INSERT INTO `+quoted+`.rag_documents(id,collection,filename,file_hash) VALUES(1,'fixture','file','hash');
 INSERT INTO `+quoted+`.rag_chunks(document_id,collection,chunk_index,content,embedding) VALUES(1,'fixture',0,'kept text','[1,0,0]')`); err != nil {
		t.Fatal(err)
	}
	dimension := 3
	fail := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "fixture failure", 400)
			return
		}
		var body struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		data := []any{}
		for i := range body.Input {
			v := make([]float32, dimension)
			v[dimension-1] = 1
			data = append(data, map[string]any{"index": i, "embedding": v})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer upstream.Close()
	embed := NewOpenAIEmbedder(upstream.URL, "fixture", "new-model")
	prepare := func(id string, confirm bool) error {
		return PrepareEmbeddingIndex(ctx, scoped, embed, dimension, id, "legacy", confirm, nil)
	}
	if err = prepare("legacy", false); err != nil {
		t.Fatal(err)
	}
	if err = prepare("new-same-dimension", false); err == nil {
		t.Fatal("changed model accepted without agreement")
	}
	if err = prepare("new-same-dimension", true); err != nil {
		t.Fatal(err)
	}
	vector := func() string {
		t.Helper()
		var s string
		if err = db.QueryRow(ctx, "SELECT embedding::text FROM "+quoted+".rag_chunks").Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if vector() != "[0,0,1]" {
		t.Fatal("same-dimension model not rebuilt")
	}
	dimension = 4
	fail = true
	if err = prepare("four-dimensional", true); err == nil {
		t.Fatal("failed provider accepted")
	}
	if vector() != "[0,0,1]" {
		t.Fatal("failed rebuild destroyed original vectors")
	}
	var id string
	db.QueryRow(ctx, "SELECT identity FROM "+quoted+".rag_embedding_meta").Scan(&id)
	if id != "new-same-dimension" {
		t.Fatal("failed rebuild changed identity")
	}
	fail = false
	if err = prepare("four-dimensional", true); err != nil {
		t.Fatal(err)
	}
	if vector() != "[0,0,0,1]" {
		t.Fatal("dimension migration failed")
	}
	var text string
	db.QueryRow(ctx, "SELECT content FROM "+quoted+".rag_chunks").Scan(&text)
	if text != "kept text" {
		t.Fatal("lost indexed text")
	}
	// Matching identity must not call the provider again at every restart.
	fail = true
	if err = prepare("four-dimensional", false); err != nil {
		t.Fatal("unnecessary reindex", err)
	}
}
