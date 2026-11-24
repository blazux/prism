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
	"prism/internal/agent"
	"prism/internal/memory"
	"prism/internal/ollama"
)

func TestAssistUsageDrainKeepsTerminalCountersAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	ch := make(chan ollama.StreamEvent, 1)
	ch <- ollama.StreamEvent{Content: "private buffered text"}
	input, output := int64(123), int64(7)
	final := &ollama.Usage{InputTokens: &input, OutputTokens: &output}
	go func() {
		<-ctx.Done()
		ch <- ollama.StreamEvent{Content: "private late text", Usage: final, Err: ctx.Err()}
		close(ch)
	}()
	cancel()
	if got := drainAssistUsage(ch, nil, time.Second); got != final {
		t.Fatalf("terminal usage after cancellation was lost: %+v", got)
	}
}

func TestAssistUsageDrainIsBoundedWhenProviderDoesNotExit(t *testing.T) {
	input := int64(12)
	known := &ollama.Usage{InputTokens: &input}
	started := time.Now()
	if got := drainAssistUsage(make(chan ollama.StreamEvent), known, time.Millisecond); got != known || time.Since(started) > time.Second {
		t.Fatalf("drain lost known usage or did not stop: %+v", got)
	}
}

func TestAssistUsageBelongsToAuthenticatedScope(t *testing.T) {
	for _, tc := range []struct {
		name, bound, wantSession string
		user, wantUser           int64
	}{
		{name: "owner", wantSession: "assistant"},
		{name: "account", user: 7, wantUser: 7, wantSession: "u7-assistant"},
		{name: "another account", user: 8, wantUser: 8, wantSession: "u8-assistant"},
		{name: "capability", user: 7, bound: "u7-project", wantUser: 7, wantSession: "u7-project"},
		{name: "group capability", user: -3, bound: "room-g3", wantSession: "room-g3"},
		{name: "group service identity", user: -3, wantSession: "room-g3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := withUser(httptest.NewRequest("POST", "/api/ai/assist?session=u999-private", nil), &memory.User{ID: tc.user})
			if tc.bound != "" {
				r = r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, tc.bound))
			}
			user, session := assistUsageIdentity(r)
			if user != tc.wantUser || session != tc.wantSession {
				t.Fatalf("wrong usage owner: %d %s", user, session)
			}
		})
	}
}

func TestAssistUsagePersistsPerRequestWithoutContent(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := t.Context()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	schema := fmt.Sprintf("assist_usage_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema+",public")
	u.RawQuery = query.Encode()
	store, err := memory.NewStore(ctx, u.String(), bytes.Repeat([]byte{7}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var mode atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 1 {
			http.Error(w, "fixture rejection", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Private rewritten text"},"finish_reason":"stop"}]}`)
		for range 2 {
			fmt.Fprintln(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":12,"prompt_tokens_details":{"cached_tokens":70}}}`)
		}
		switch mode.Load() {
		case 2:
			fmt.Fprintln(w, `data: {"error":{"message":"fixture interrupted"}}`)
		case 3:
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			fmt.Fprintln(w, "data: [DONE]")
		}
	}))
	defer upstream.Close()
	s := &Server{memStore: store, cfg: Config{LLMBackend: "openai", OpenAIBaseURL: upstream.URL, Model: "fixture"}}
	for i, status := range []int{200, 200, 502, 502, 502} {
		if i < 2 {
			mode.Store(0)
		} else {
			mode.Store(int32(i - 1))
		}
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest("POST", "/api/ai/assist?session=u999-private", strings.NewReader(`{"task":"doc_rewrite","text":"Private source text"}`)), &memory.User{ID: 7})
		if mode.Load() == 3 {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
		s.handleAIAssist(w, r)
		if w.Code != status {
			t.Fatalf("unexpected assist result: %d %s", w.Code, w.Body.String())
		}
	}
	rows, err := db.Query(ctx, "SELECT user_id,session,item,meta->'measurement' FROM "+quoted+".usage_events WHERE kind='model_request' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	tasks := map[string]bool{}
	for rows.Next() {
		var user int64
		var session, model string
		var raw json.RawMessage
		if err := rows.Scan(&user, &session, &model, &raw); err != nil {
			t.Fatal(err)
		}
		var record agent.ModelUsage
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if user != 7 || session != "u7-assistant" || model != "fixture" || record.Scope != "assist" || record.TaskID == "" || tasks[record.TaskID] || strings.Contains(string(raw), "Private") {
			t.Fatalf("wrong attribution or leaked content: user=%d session=%s model=%s meta=%s", user, session, model, raw)
		}
		tasks[record.TaskID] = true
		if record.Complete != (count < 2) {
			t.Fatalf("wrong completion status: %+v", record)
		}
		if count == 2 {
			if record.Usage != nil {
				t.Fatalf("failed request fabricated provider counters: %+v", record)
			}
		} else if record.Usage == nil || record.Usage.InputTokens == nil || record.Usage.OutputTokens == nil || record.Usage.CacheReadTokens == nil ||
			*record.Usage.InputTokens != 100 || *record.Usage.OutputTokens != 12 || *record.Usage.CacheReadTokens != 70 {
			t.Fatalf("usage duplicated or missing: %+v", record)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 5 {
		t.Fatalf("expected one record per request, got %d (%v)", count, err)
	}
}
