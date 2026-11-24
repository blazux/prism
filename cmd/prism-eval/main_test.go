package main

import (
	"encoding/json"
	"math"
	"testing"
)

func TestScoreUsagePreservesCacheTiersAndPartialCounts(t *testing.T) {
	r := &Result{ModelRequests: []json.RawMessage{
		json.RawMessage(`{"model":"claude-opus-4-6","usage":{"input_tokens":1000,"output_tokens":20,"cache_read_tokens":100,"cache_write_tokens":300,"cache_write_5m_tokens":100,"cache_write_1h_tokens":200}}`),
		json.RawMessage(`{"model":"claude-opus-4-6","usage":{"input_tokens":1000,"output_tokens":10,"cache_read_tokens":200,"cache_write_tokens":100}}`),
		json.RawMessage(`{"model":"claude-opus-4-6","usage":{"input_tokens":500,"cache_write_tokens":50,"cache_write_1h_tokens":50}}`),
		json.RawMessage(`{"model":"claude-opus-4-6","usage":{"input_tokens":1000,"output_tokens":30,"cache_write_tokens":200,"cache_write_5m_tokens":100,"cache_write_pricing_unknown":true}}`),
		json.RawMessage(`{"model":"claude-opus-4-6","usage":{"input_tokens":0,"output_tokens":0}}`),
	}}
	scoreUsage(r)
	if r.PricedRequests != 3 || r.UnpricedRequests != 2 || math.Abs(r.EstimatedCostUSD-0.01065) > 1e-12 ||
		r.InputTokens != 3500 || r.OutputTokens != 60 || r.CacheReadTokens != 300 || r.CacheWriteTokens != 650 ||
		r.CacheWrite5mTokens != 300 || r.CacheWrite1hTokens != 250 {
		t.Fatalf("wrong TTL or partial-counter score: %+v", r)
	}
}

func TestScoreUsagePreservesUnknownCalls(t *testing.T) {
	r := &Result{ModelRequests: []json.RawMessage{
		json.RawMessage(`{"model":"claude-opus-4-6","usage":{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500,"cache_write_tokens":0}}`),
		json.RawMessage(`{"model":"private-model","usage":null}`),
	}}
	scoreUsage(r)
	if r.PricedRequests != 1 || r.UnpricedRequests != 1 || r.EstimatedCostUSD <= 0 || r.CacheReadTokens != 500 {
		t.Fatalf("wrong score: %+v", r)
	}
}

func TestScoreUsagePricesGPTPerRequestAndKeepsLocalModelUnknown(t *testing.T) {
	r := &Result{ModelRequests: []json.RawMessage{
		json.RawMessage(`{"model":"source::gpt-5.5","usage":{"input_tokens":200000,"output_tokens":50,"reasoning_tokens":40,"cache_read_tokens":100000,"pricing_provider":"openai","service_tier":"default"}}`),
		json.RawMessage(`{"model":"source::gpt-5.5","usage":{"input_tokens":200000,"output_tokens":50,"cache_read_tokens":100000,"pricing_provider":"openai","service_tier":"default"}}`),
		json.RawMessage(`{"model":"source::gpt-5.5","usage":{"input_tokens":300000,"output_tokens":100,"cache_read_tokens":100000,"pricing_provider":"openai","service_tier":"default"}}`),
		json.RawMessage(`{"model":"local::gpt-5.5","usage":{"input_tokens":1000,"output_tokens":100,"cache_read_tokens":500}}`),
	}}
	scoreUsage(r)
	if r.PricedRequests != 3 || r.UnpricedRequests != 1 || math.Abs(r.EstimatedCostUSD-3.2075) > 1e-12 ||
		r.InputTokens != 701000 || r.OutputTokens != 300 || r.CacheReadTokens != 300500 {
		t.Fatalf("wrong GPT per-request pricing or provenance: %+v", r)
	}
}

func TestDisabledNativeToolsKeepsOnlyAllowlisted(t *testing.T) {
	disabled := disabledNativeTools([]string{"widget", "cron"})
	blocked := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		blocked[name] = true
	}
	if blocked["widget"] || blocked["cron"] || !blocked["docker_run"] || !blocked["agent_settings"] {
		t.Fatalf("unexpected native tool filter: %v", disabled)
	}
}
