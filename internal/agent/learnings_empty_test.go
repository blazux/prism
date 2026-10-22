package agent

import (
	"context"
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
	"prism/internal/rag"
)

func TestEmptyLearningsSkipEmbeddingAndSeeFirstDocument(t *testing.T) {
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
	schema := pgx.Identifier{fmt.Sprintf("learnings_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", strings.Trim(schema, "\"")+",public")
	u.RawQuery = q.Encode()
	store, err := rag.NewStore(ctx, u.String(), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var embeds atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embeds.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1,0,0]}]}`)
	}))
	defer provider.Close()
	e := &ToolExecutor{personalScopeOverride: "u1"}
	e.SetRAG(store, rag.NewOpenAIEmbedder(provider.URL, "", "fixture"), nil)
	lookup := func() string { return e.SearchLearnings(ctx, "synthetic question") }
	if lookup() != "" || embeds.Load() != 0 {
		t.Fatal("empty learning memory called provider")
	}
	other := "u2--" + learningsCollection
	if err := store.UpsertDocument(ctx, other, "other", "hash", 1, []string{"OTHER_USER"}, []int{1}, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	if lookup() != "" || embeds.Load() != 0 {
		t.Fatal("another user's memory caused an embedding request")
	}
	collection := e.pcol(learningsCollection)
	if err := store.UpsertDocument(ctx, collection, "first", "hash", 1, []string{"FIRST_LEARNING"}, []int{1}, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	if got := lookup(); !strings.Contains(got, "FIRST_LEARNING") || strings.Contains(got, "OTHER_USER") || embeds.Load() != 1 {
		t.Fatal("first learning was not immediately retrieved within its scope")
	}
	if err := store.DeleteCollection(ctx, collection); err != nil {
		t.Fatal(err)
	}
	if lookup() != "" || embeds.Load() != 1 {
		t.Fatal("deleted collection kept calling provider")
	}
}
