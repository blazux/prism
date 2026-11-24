package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"prism/internal/memory"
)

func TestTaskCostsAggregateMixedCacheTiersAndPartialUsage(t *testing.T) {
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
	schema := fmt.Sprintf("usage_cost_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	store, err := memory.NewStore(ctx, u.String(), bytes.Repeat([]byte{7}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, usage := range []string{
		`{"input_tokens":1000,"output_tokens":10,"cache_read_tokens":200,"cache_write_tokens":100}`,
		`{"input_tokens":1000,"output_tokens":20,"cache_read_tokens":100,"cache_write_tokens":300,"cache_write_5m_tokens":100,"cache_write_1h_tokens":200}`,
		`{"input_tokens":500,"cache_read_tokens":100,"cache_write_tokens":50,"cache_write_1h_tokens":50}`,
		`{"input_tokens":1000,"output_tokens":30,"cache_read_tokens":50,"cache_write_tokens":200,"cache_write_5m_tokens":100,"cache_write_pricing_unknown":true}`,
		`null`,
		`{"input_tokens":0,"output_tokens":0}`,
	} {
		store.AddUsage(ctx, 7, "u7-chat", "model_request", "claude-opus-4-6", 1, map[string]interface{}{
			"measurement": map[string]interface{}{"taskId": "task", "scope": "research", "usage": json.RawMessage(usage)},
		})
	}
	store.AddUsage(ctx, 7, "u7-chat", "model_request", "claude-opus-4-6", 1, map[string]interface{}{
		"measurement": map[string]interface{}{"usage": json.RawMessage(`{"input_tokens":100,"output_tokens":1}`)},
	})
	groups, err := store.RecentModelUsageGroups(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	summaries := summarizeTaskCosts(groups)
	if len(summaries) != 2 {
		t.Fatalf("task and legacy usage were mixed: %+v", summaries)
	}
	var task *TaskCostSummary
	for i := range summaries {
		if summaries[i].TaskID == "task" {
			task = &summaries[i]
		} else if !summaries[i].Legacy || summaries[i].TaskID != "legacy:u7-chat" || summaries[i].Calls != 1 {
			t.Fatalf("wrong legacy identity: %+v", summaries[i])
		}
	}
	if task == nil || task.Calls != 6 || task.UnpricedCalls != 3 || len(task.Models) != 1 || task.Legacy ||
		task.InputTokens != 3500 || task.OutputTokens != 60 || task.CacheReadTokens != 450 || task.CacheWriteTokens != 650 ||
		task.CacheWrite5mTokens != 300 || task.CacheWrite1hTokens != 250 || math.Abs(task.KnownCostUSD-0.01065) > 1e-12 {
		t.Fatalf("wrong totals or incomplete usage was priced: %+v", task)
	}
	requests, err := store.ModelUsageRequests(ctx, "u7-chat", "task", false)
	if err != nil || len(requests) != 6 {
		t.Fatalf("wrong task trace: %d (%v)", len(requests), err)
	}
	trace := traceTaskCost(requests)
	if math.Abs(trace[len(trace)-1].CumulativeCostUSD-task.KnownCostUSD) > 1e-12 {
		t.Fatalf("trace and aggregate costs differ: %+v / %+v", trace, task)
	}
	legacy, err := store.ModelUsageRequests(ctx, "u7-chat", "legacy:u7-chat", true)
	if err != nil || len(legacy) != 1 {
		t.Fatalf("wrong legacy trace: %d (%v)", len(legacy), err)
	}

	// Context pricing is selected per request before aggregation. A different
	// service tier, missing cache count or compatible/local provenance stays unknown.
	for _, usage := range []string{
		`{"input_tokens":200000,"output_tokens":50,"cache_read_tokens":100000,"pricing_provider":"openai","service_tier":"default"}`,
		`{"input_tokens":200000,"output_tokens":50,"cache_read_tokens":100000,"pricing_provider":"openai","service_tier":"default"}`,
		`{"input_tokens":300000,"output_tokens":100,"cache_read_tokens":100000,"pricing_provider":"openai","service_tier":"default"}`,
		`{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500,"pricing_provider":"openai","service_tier":"priority"}`,
		`{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500}`,
		`{"input_tokens":1000,"output_tokens":100,"pricing_provider":"openai","service_tier":"default"}`,
	} {
		store.AddUsage(ctx, 8, "u8-chat", "model_request", "gpt-5.5", 1, map[string]interface{}{
			"measurement": map[string]interface{}{"taskId": "gpt-task", "scope": "chat", "usage": json.RawMessage(usage)},
		})
	}
	groups, err = store.RecentModelUsageGroups(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	var gptGroups []memory.ModelUsageGroup
	for _, group := range groups {
		if group.TaskID == "gpt-task" {
			gptGroups = append(gptGroups, group)
		}
	}
	gptSummary := summarizeTaskCosts(gptGroups)
	if len(gptGroups) != 5 || len(gptSummary) != 1 || gptSummary[0].Calls != 6 ||
		gptSummary[0].UnpricedCalls != 3 || math.Abs(gptSummary[0].KnownCostUSD-3.2075) > 1e-12 {
		t.Fatalf("SQL merged GPT context bands or billing provenance: %+v / %+v", gptGroups, gptSummary)
	}
	gptRequests, err := store.ModelUsageRequests(ctx, "u8-chat", "gpt-task", false)
	if err != nil || len(gptRequests) != 6 {
		t.Fatalf("wrong GPT trace: %d (%v)", len(gptRequests), err)
	}
	gptTrace := traceTaskCost(gptRequests)
	if math.Abs(gptTrace[len(gptTrace)-1].CumulativeCostUSD-gptSummary[0].KnownCostUSD) > 1e-12 {
		t.Fatalf("GPT trace and aggregate costs differ: %+v / %+v", gptTrace, gptSummary)
	}
}
