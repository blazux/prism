package server

import (
	"bytes"
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"prism/internal/memory"
	"prism/internal/ollama"
	"prism/internal/rag"
	"testing"
	"time"
)

func TestModelsRemainAvailableWhenPrimaryFails(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "fixture unavailable", 503) }))
	defer primary.Close()
	secondaryCalls := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"fixture-working-model"}]}`))
	}))
	defer secondary.Close()
	s := &Server{cfg: Config{LLMBackend: "openai", OpenAIBaseURL: primary.URL, OllamaURL: secondary.URL}}
	models, err := s.chatModels(context.Background())
	if err != nil || len(models) != 1 || models[0] != "fixture-working-model" || secondaryCalls != 1 {
		t.Fatalf("behavior changed: models=%v err=%v secondaryCalls=%d", models, err, secondaryCalls)
	}
	if _, ok := s.chatBackendFor("fixture-working-model").(*ollama.Client); !ok {
		t.Fatal("healthy model routed to unavailable primary")
	}
}
func TestReviewPersistenceAndSessionDeletion(t *testing.T) {
	dsn := os.Getenv("PRISM_REVIEW_DB")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	schema := pgx.Identifier{fmt.Sprintf("final_fixes_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", schema+",public")
	parsed.RawQuery = q.Encode()
	dsn = parsed.String()
	key := bytes.Repeat([]byte{9}, 32)
	ms, err := memory.NewStore(ctx, dsn, key, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = ms.UpsertSession(ctx, "review-persistent", "Review fixture", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = ms.AppendMessage(ctx, "review-persistent", "user", "dummy persisted message", nil); err != nil {
		t.Fatal(err)
	}
	if err = ms.SetScriptSecret(ctx, "review_key", "dummy credential"); err != nil {
		t.Fatal(err)
	}
	if err = ms.UpsertSession(ctx, "review-deleted", "Deleted fixture", nil); err != nil {
		t.Fatal(err)
	}
	if err = ms.SetLiveCompaction(ctx, "review-deleted", memory.LiveCompaction{Summary: "obsolete dummy summary", BeforeID: 1}); err != nil {
		t.Fatal(err)
	}
	if err = ms.DeleteSession(ctx, "review-deleted"); err != nil {
		t.Fatal(err)
	}
	if ms.GetLiveCompaction(ctx, "review-deleted") != nil {
		t.Fatal("deleted compaction must be cleaned")
	}

	ms.Close()
	ms, err = memory.NewStore(ctx, dsn, key, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	hist, err := ms.LoadHistory(ctx, "review-persistent")
	if err != nil || len(hist) != 1 {
		t.Fatal("history restart failure", err)
	}
	value, exists, err := ms.GetSecret(ctx, "review_key")
	if err != nil || !exists || value != "dummy credential" {
		t.Fatal("secret restart failure", err)
	}
	t.Log("VERIFIED: history and encrypted secret survive store reopen")
	rs, err := rag.NewStore(ctx, dsn, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = rs.EnsureCollection(ctx, "review-docs", "global"); err != nil {
		t.Fatal(err)
	}
	if err = rs.UpsertDocument(ctx, "review-docs", "fixture.txt", "fixture-hash", 12, []string{"dummy durable knowledge"}, []int{1}, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	if err = rs.UpsertDocument(ctx, "review-docs", "fixture.txt", "broken", 12, []string{"broken replacement"}, []int{1}, [][]float32{{1, 0}}); err == nil {
		t.Fatal("wrong dimension accepted")
	}
	results, err := rs.Search(ctx, "review-docs", []float32{1, 0, 0}, 3)
	if err != nil || len(results) != 1 || results[0].Content != "dummy durable knowledge" {
		t.Fatal("failed replacement damaged document", err)
	}
	rs.Close()
	t.Log("VERIFIED: failed RAG replacement rolls back to original document")
	wrong, err := rag.NewStore(ctx, dsn, 4)
	if err == nil {
		wrong.Close()
		t.Fatal("incompatible embedding dimension accepted")
	}
}
