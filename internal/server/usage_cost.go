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
	InputTokens              int64     `json:"inputTokens"`
	OutputTokens             int64     `json:"outputTokens"`
	CacheReadTokens          int64     `json:"cacheReadTokens"`
	CacheWriteTokens         int64     `json:"cacheWriteTokens"`
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
		if m.Usage != nil && m.Usage.InputTokens != nil && m.Usage.OutputTokens != nil {
			call.InputTokens, call.OutputTokens = *m.Usage.InputTokens, *m.Usage.OutputTokens
			if m.Usage.CacheReadTokens != nil {
				call.CacheReadTokens = *m.Usage.CacheReadTokens
			}
			if m.Usage.CacheWriteTokens != nil {
				call.CacheWriteTokens = *m.Usage.CacheWriteTokens
			}
			call.CostUSD, call.CostKnown = usagecost.Estimate(request.Model, usagecost.Tokens{
				Input: call.InputTokens, Output: call.OutputTokens, CacheRead: call.CacheReadTokens, CacheWrite: call.CacheWriteTokens,
			})
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
	TaskID           string    `json:"taskId"`
	Session          string    `json:"session"`
	UserID           int64     `json:"userId"`
	Legacy           bool      `json:"legacy"`
	Models           []string  `json:"models"`
	Calls            int64     `json:"calls"`
	UnpricedCalls    int64     `json:"unpricedCalls"`
	InputTokens      int64     `json:"inputTokens"`
	OutputTokens     int64     `json:"outputTokens"`
	CacheReadTokens  int64     `json:"cacheReadTokens"`
	CacheWriteTokens int64     `json:"cacheWriteTokens"`
	MaxToolBytes     int64     `json:"maxToolBytes"`
	KnownCostUSD     float64   `json:"knownCostUsd"`
	First            time.Time `json:"first"`
	Last             time.Time `json:"last"`
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
		if g.MaxToolBytes > t.MaxToolBytes {
			t.MaxToolBytes = g.MaxToolBytes
		}
		if g.First.Before(t.First) {
			t.First = g.First
		}
		if g.Last.After(t.Last) {
			t.Last = g.Last
		}
		if cost, ok := usagecost.Estimate(g.Model, usagecost.Tokens{Input: g.Input, Output: g.Output, CacheRead: g.CacheRead, CacheWrite: g.CacheWrite}); ok {
			t.KnownCostUSD += cost
		} else {
			t.UnpricedCalls += g.Calls - g.Missing
		}
	}
	out := make([]TaskCostSummary, 0, len(byKey))
	for _, t := range byKey {
		sort.Strings(t.Models)
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}
