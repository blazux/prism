package server

import (
	"encoding/json"
	"testing"
	"time"

	"prism/internal/memory"
)

func TestSummarizeTaskCostsCombinesModelsAndMarksUnknown(t *testing.T) {
	now := time.Now()
	groups := []memory.ModelUsageGroup{
		{UserID: 3, Session: "u3-chat", TaskID: "42", Model: "claude-opus-4-6", Calls: 2, Input: 2000, Output: 100, CacheRead: 1000, First: now, Last: now},
		{UserID: 3, Session: "u3-chat", TaskID: "42", Model: "private-model", Calls: 1, Missing: 1, First: now, Last: now},
	}
	got := summarizeTaskCosts(groups)
	if len(got) != 1 || got[0].Calls != 3 || got[0].UnpricedCalls != 1 || got[0].KnownCostUSD <= 0 || got[0].CacheReadTokens != 1000 {
		t.Fatalf("wrong summary: %+v", got)
	}
}

func TestTraceTaskCostCumulative(t *testing.T) {
	now := time.Now()
	requests := []memory.ModelUsageRequest{
		{TS: now, Model: "anthropic::claude-opus-4-6", Measurement: json.RawMessage(`{"usage":{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500,"cache_write_tokens":0}}`)},
		{TS: now, Model: "private-model", Measurement: json.RawMessage(`{"usage":null}`)},
	}
	got := traceTaskCost(requests)
	if len(got) != 2 || !got[0].CostKnown || got[0].CostUSD <= 0 || got[1].CostKnown || got[1].CumulativeCostUSD != got[0].CostUSD {
		t.Fatalf("wrong trace: %+v", got)
	}
}
