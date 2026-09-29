package main

import (
	"encoding/json"
	"testing"
)

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
