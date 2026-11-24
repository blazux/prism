package server

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"prism/internal/memory"
)

func TestTraceTaskCostPreservesTiersAndPartialCounters(t *testing.T) {
	requests := []memory.ModelUsageRequest{
		{Model: "claude-opus-4-6", Measurement: json.RawMessage(`{"scope":"compaction","usage":{"input_tokens":1000,"output_tokens":20,"cache_read_tokens":100,"cache_write_tokens":300,"cache_write_5m_tokens":100,"cache_write_1h_tokens":200}}`)},
		{Model: "claude-opus-4-6", Measurement: json.RawMessage(`{"scope":"research","usage":{"input_tokens":500,"cache_write_tokens":50,"cache_write_1h_tokens":50}}`)},
		{Model: "claude-opus-4-6", Measurement: json.RawMessage(`{"scope":"assist","usage":{"input_tokens":1000,"output_tokens":30,"cache_write_tokens":200,"cache_write_5m_tokens":100,"cache_write_pricing_unknown":true}}`)},
		{Model: "claude-opus-4-6", Measurement: json.RawMessage(`{"usage":{"input_tokens":0,"output_tokens":0}}`)},
	}
	got := traceTaskCost(requests)
	if len(got) != 4 || !got[0].CostKnown || math.Abs(got[0].CostUSD-0.006175) > 1e-12 ||
		got[0].CacheWrite5mTokens != 100 || got[0].CacheWrite1hTokens != 200 || got[0].Scope != "compaction" {
		t.Fatalf("wrong mixed TTL trace: %+v", got)
	}
	if got[1].CostKnown || got[1].InputTokens != 500 || got[1].CacheWrite1hTokens != 50 || got[1].CumulativeCostUSD != got[0].CostUSD ||
		got[2].CostKnown || !got[2].CacheWritePricingUnknown || !got[3].CostKnown || got[3].CostUSD != 0 {
		t.Fatalf("partial or unknown counters were priced or discarded: %+v", got)
	}
	if !got[1].InputTokensKnown || got[1].OutputTokensKnown || !got[3].InputTokensKnown || !got[3].OutputTokensKnown || got[3].CacheReadTokensKnown || got[3].CacheWriteTokensKnown {
		t.Fatalf("missing counters became explicit zeroes: %+v", got)
	}
}

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
		{TS: now, Model: "anthropic::claude-opus-4-6", Measurement: json.RawMessage(`{"scope":"subagent","history_content_bytes":100,"history_tool_result_bytes":50,"history_tool_argument_bytes":25,"history_image_bytes":4,"usage":{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500,"cache_write_tokens":0}}`)},
		{TS: now, Model: "private-model", Measurement: json.RawMessage(`{"usage":null}`)},
	}
	got := traceTaskCost(requests)
	if len(got) != 2 || !got[0].CostKnown || got[0].CostUSD <= 0 || got[1].CostKnown || got[1].CumulativeCostUSD != got[0].CostUSD {
		t.Fatalf("wrong trace: %+v", got)
	}
	if got[0].Scope != "subagent" || got[0].HistoryContentBytes != 100 || got[0].HistoryToolResultBytes != 50 || got[0].HistoryToolArgumentBytes != 25 || got[0].HistoryImageBytes != 4 {
		t.Fatalf("wrong trace: %+v", got)
	}
}

func TestOpenAICostTraceRequiresProvenanceTierAndCacheCounter(t *testing.T) {
	requests := []memory.ModelUsageRequest{
		{Model: "source::gpt-5.5", Measurement: json.RawMessage(`{"usage":{"input_tokens":1000,"output_tokens":100,"reasoning_tokens":80,"cache_read_tokens":500,"pricing_provider":"openai","service_tier":"default"}}`)},
		{Model: "gpt-5.5", Measurement: json.RawMessage(`{"usage":{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500}}`)},
		{Model: "gpt-5.5", Measurement: json.RawMessage(`{"usage":{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500,"pricing_provider":"openai","service_tier":"priority"}}`)},
		{Model: "gpt-5.5", Measurement: json.RawMessage(`{"usage":{"input_tokens":1000,"output_tokens":100,"pricing_provider":"openai","service_tier":"default"}}`)},
	}
	got := traceTaskCost(requests)
	// Reasoning is already included in output_tokens and is not added again.
	if !got[0].CostKnown || math.Abs(got[0].CostUSD-.00575) > 1e-12 ||
		got[0].PricingProvider != "openai" || got[0].ServiceTier != "default" {
		t.Fatalf("wrong standard API price/provenance: %+v", got[0])
	}
	for _, call := range got[1:] {
		if call.CostKnown || call.CumulativeCostUSD != got[0].CostUSD {
			t.Fatalf("unknown tariff or local model presented as billed zero: %+v", call)
		}
	}
}

func TestSummarizeOpenAIPreservesPerRequestContextTariffs(t *testing.T) {
	now := time.Now()
	groups := []memory.ModelUsageGroup{
		{UserID: 1, Session: "u1-chat", TaskID: "gpt", Model: "gpt-5.5", Calls: 2,
			PricingProvider: "openai", ServiceTier: "default", CacheReadKnown: true,
			Input: 400000, Output: 100, CacheRead: 200000, RequestInputMax: 200000, First: now, Last: now},
		{UserID: 1, Session: "u1-chat", TaskID: "gpt", Model: "gpt-5.5", Calls: 1,
			PricingProvider: "openai", ServiceTier: "default", CacheReadKnown: true,
			Input: 300000, Output: 100, CacheRead: 100000, RequestInputMax: 300000, First: now, Last: now},
		{UserID: 1, Session: "u1-chat", TaskID: "gpt", Model: "gpt-5.5", Calls: 1,
			Input: 1000, Output: 100, CacheRead: 500, RequestInputMax: 1000, First: now, Last: now},
	}
	got := summarizeTaskCosts(groups)
	// Two short calls = 1.103; one long call = 2.1045. Last call has no provenance.
	if len(got) != 1 || got[0].Calls != 4 || got[0].UnpricedCalls != 1 ||
		math.Abs(got[0].KnownCostUSD-3.2075) > 1e-12 {
		t.Fatalf("wrong mixed-context/provenance price: %+v", got)
	}
}
