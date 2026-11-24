package server

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
	"prism/internal/usagecost"
)

type TaskCostCall struct {
	TS                       time.Time `json:"ts"`
	Model                    string    `json:"model"`
	Scope                    string    `json:"scope"`
	PricingProvider          string    `json:"pricingProvider,omitempty"`
	ServiceTier              string    `json:"serviceTier,omitempty"`
	InputTokens              int64     `json:"inputTokens"`
	OutputTokens             int64     `json:"outputTokens"`
	InputTokensKnown         bool      `json:"inputTokensKnown"`
	OutputTokensKnown        bool      `json:"outputTokensKnown"`
	CacheReadTokensKnown     bool      `json:"cacheReadTokensKnown"`
	CacheWriteTokensKnown    bool      `json:"cacheWriteTokensKnown"`
	CacheReadTokens          int64     `json:"cacheReadTokens"`
	CacheWriteTokens         int64     `json:"cacheWriteTokens"`
	CacheWrite5mTokens       int64     `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens       int64     `json:"cacheWrite1hTokens"`
	CacheWritePricingUnknown bool      `json:"cacheWritePricingUnknown"`
	SystemBytes              int       `json:"systemBytes"`
	ToolBytes                int       `json:"toolBytes"`
	MessageCount             int       `json:"messageCount"`
	HistoryContentBytes      int       `json:"historyContentBytes"`
	HistoryToolResultBytes   int       `json:"historyToolResultBytes"`
	HistoryToolArgumentBytes int       `json:"historyToolArgumentBytes"`
	HistoryImageBytes        int       `json:"historyImageBytes"`
	CostUSD                  float64   `json:"costUsd"`
	CumulativeCostUSD        float64   `json:"cumulativeCostUsd"`
	CostKnown                bool      `json:"costKnown"`
}

func traceTaskCost(requests []memory.ModelUsageRequest) []TaskCostCall {
	out := make([]TaskCostCall, 0, len(requests))
	cumulative := 0.0
	for _, request := range requests {
		var m agent.ModelUsage
		_ = json.Unmarshal(request.Measurement, &m)
		call := TaskCostCall{TS: request.TS, Model: request.Model, Scope: m.Scope, SystemBytes: m.SystemBytes,
			ToolBytes: m.ToolBytes, MessageCount: m.MessageCount, HistoryContentBytes: m.HistoryContentBytes,
			HistoryToolResultBytes: m.HistoryToolResultBytes, HistoryToolArgumentBytes: m.HistoryToolArgumentBytes,
			HistoryImageBytes: m.HistoryImageBytes}
		if m.Usage != nil {
			call.PricingProvider = m.Usage.PricingProvider
			call.ServiceTier = m.Usage.ServiceTier
			call.InputTokensKnown = m.Usage.InputTokens != nil
			call.OutputTokensKnown = m.Usage.OutputTokens != nil
			call.CacheReadTokensKnown = m.Usage.CacheReadTokens != nil
			call.CacheWriteTokensKnown = m.Usage.CacheWriteTokens != nil
			if m.Usage.InputTokens != nil {
				call.InputTokens = *m.Usage.InputTokens
			}
			if m.Usage.OutputTokens != nil {
				call.OutputTokens = *m.Usage.OutputTokens
			}
			if m.Usage.CacheReadTokens != nil {
				call.CacheReadTokens = *m.Usage.CacheReadTokens
			}
			if m.Usage.CacheWriteTokens != nil {
				call.CacheWriteTokens = *m.Usage.CacheWriteTokens
			}
			if m.Usage.CacheWrite5mTokens == nil && m.Usage.CacheWrite1hTokens == nil && !m.Usage.CacheWritePricingUnknown {
				call.CacheWrite5mTokens = call.CacheWriteTokens
			}
			if m.Usage.CacheWrite5mTokens != nil {
				call.CacheWrite5mTokens = *m.Usage.CacheWrite5mTokens
			}
			if m.Usage.CacheWrite1hTokens != nil {
				call.CacheWrite1hTokens = *m.Usage.CacheWrite1hTokens
			}
			call.CacheWritePricingUnknown = m.Usage.CacheWritePricingUnknown
			if m.Usage.InputTokens != nil && m.Usage.OutputTokens != nil {
				call.CostUSD, call.CostKnown = usagecost.Estimate(request.Model, usagecost.Tokens{
					Input: call.InputTokens, Output: call.OutputTokens, CacheRead: call.CacheReadTokens, CacheWrite: call.CacheWriteTokens,
					CacheWrite5m: call.CacheWrite5mTokens, CacheWrite1h: call.CacheWrite1hTokens, CacheWritePricingUnknown: call.CacheWritePricingUnknown,
					PricingProvider: call.PricingProvider, ServiceTier: call.ServiceTier, CacheReadKnown: call.CacheReadTokensKnown,
				})
			}
		}
		cumulative += call.CostUSD
		call.CumulativeCostUSD = cumulative
		out = append(out, call)
	}
	return out
}

// TaskCostSummary is deliberately content-free: it can explain repeated
// model calls and cache efficiency without exposing prompts or tool results.
type TaskCostSummary struct {
	TaskID             string    `json:"taskId"`
	Session            string    `json:"session"`
	UserID             int64     `json:"userId"`
	Legacy             bool      `json:"legacy"`
	Models             []string  `json:"models"`
	Calls              int64     `json:"calls"`
	UnpricedCalls      int64     `json:"unpricedCalls"`
	InputTokens        int64     `json:"inputTokens"`
	OutputTokens       int64     `json:"outputTokens"`
	CacheReadTokens    int64     `json:"cacheReadTokens"`
	CacheWriteTokens   int64     `json:"cacheWriteTokens"`
	CacheWrite5mTokens int64     `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64     `json:"cacheWrite1hTokens"`
	MaxToolBytes       int64     `json:"maxToolBytes"`
	KnownCostUSD       float64   `json:"knownCostUsd"`
	First              time.Time `json:"first"`
	Last               time.Time `json:"last"`
}

func summarizeTaskCosts(groups []memory.ModelUsageGroup) []TaskCostSummary {
	byKey := make(map[string]*TaskCostSummary)
	for _, g := range groups {
		key := strconv.FormatInt(g.UserID, 10) + "\x00" + g.Session + "\x00" + g.TaskID
		t := byKey[key]
		if t == nil {
			t = &TaskCostSummary{TaskID: g.TaskID, Session: g.Session, UserID: g.UserID, Legacy: g.Legacy, Models: []string{}, First: g.First, Last: g.Last}
			byKey[key] = t
		}
		t.Models = append(t.Models, g.Model)
		t.Calls += g.Calls
		t.UnpricedCalls += g.Missing
		t.InputTokens += g.Input
		t.OutputTokens += g.Output
		t.CacheReadTokens += g.CacheRead
		t.CacheWriteTokens += g.CacheWrite
		five := g.CacheWrite5m
		if five == 0 && g.CacheWrite1h == 0 && !g.CacheWritePricingUnknown {
			five = g.CacheWrite
		}
		t.CacheWrite5mTokens += five
		t.CacheWrite1hTokens += g.CacheWrite1h
		if g.MaxToolBytes > t.MaxToolBytes {
			t.MaxToolBytes = g.MaxToolBytes
		}
		if g.First.Before(t.First) {
			t.First = g.First
		}
		if g.Last.After(t.Last) {
			t.Last = g.Last
		}
		if cost, ok := usagecost.EstimateForContext(g.Model, usagecost.Tokens{
			Input: g.Input, Output: g.Output, CacheRead: g.CacheRead, CacheWrite: g.CacheWrite,
			CacheWrite5m: g.CacheWrite5m, CacheWrite1h: g.CacheWrite1h, CacheWritePricingUnknown: g.CacheWritePricingUnknown,
			PricingProvider: g.PricingProvider, ServiceTier: g.ServiceTier, CacheReadKnown: g.CacheReadKnown,
		}, g.RequestInputMax); ok && g.Missing == 0 {
			t.KnownCostUSD += cost
		} else {
			t.UnpricedCalls += g.Calls - g.Missing
		}
	}
	out := make([]TaskCostSummary, 0, len(byKey))
	for _, t := range byKey {
		sort.Strings(t.Models)
		unique := t.Models[:0]
		for _, model := range t.Models {
			if len(unique) == 0 || unique[len(unique)-1] != model {
				unique = append(unique, model)
			}
		}
		t.Models = unique
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}
