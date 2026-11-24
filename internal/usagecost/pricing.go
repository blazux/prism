// Package usagecost estimates standard API spend from provider-reported
// token counts. It is diagnostic, not a provider invoice or a negotiated gateway rate.
package usagecost

import (
	"strings"
	"time"
)

// Prices checked against https://platform.claude.com/docs/en/models/ on
// 2026-11-23, and https://developers.openai.com/api/docs/pricing.md on the same
// date. Rates are USD per million tokens; Claude CacheWrite is the five-minute rate.
const PricingAsOf = "2026-11-23"

type Rates struct {
	Input, Output, CacheWrite, CacheRead float64
}

type Tokens struct {
	Input, Output, CacheRead, CacheWrite int64
	CacheWrite5m, CacheWrite1h           int64
	CacheWritePricingUnknown             bool
	PricingProvider, ServiceTier         string
	CacheReadKnown                       bool
}

func RatesFor(model string) (Rates, bool) {
	model = rawModel(model)
	for _, p := range []struct {
		id string
		r  Rates
	}{
		{"claude-opus-4-6", Rates{5, 25, 6.25, 0.50}},
		{"claude-sonnet-4-6", Rates{3, 15, 3.75, 0.30}},
		{"claude-sonnet-5-5", Rates{2, 10, 2.50, 0.10}},
		{"claude-sonnet-5", Rates{2, 10, 2.50, 0.20}},
	} {
		// A dated snapshot is the same model; a different minor version isn't.
		if model == p.id || strings.HasPrefix(model, p.id+"-20") {
			return p.r, true
		}
	}
	if r, _, ok := openAIRates(model); ok {
		return r, true
	}
	return Rates{}, false
}

func rawModel(model string) string {
	if _, raw, qualified := strings.Cut(model, "::"); qualified {
		return raw
	}
	return model
}

// Match a known family or its dated API snapshot, never another minor version,
// a chat/pro variant, or a fine-tuned/local alias with a similar prefix.
func sameSnapshot(model, family string) bool {
	if model == family {
		return true
	}
	date, ok := strings.CutPrefix(model, family+"-")
	if !ok || len(date) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse("2006-01-02", date)
	return err == nil && date <= PricingAsOf
}

func openAIRates(model string) (Rates, bool, bool) {
	for _, p := range []struct {
		id   string
		r    Rates
		long bool
	}{
		{"gpt-5.5", Rates{Input: 5, Output: 30, CacheRead: .5}, true},
		{"gpt-5.4", Rates{Input: 2.5, Output: 15, CacheRead: .25}, true},
		{"gpt-5.4-mini", Rates{Input: .75, Output: 4.5, CacheRead: .075}, false},
		{"gpt-5.4-nano", Rates{Input: .2, Output: 1.25, CacheRead: .02}, false},
		{"gpt-5.2", Rates{Input: 1.75, Output: 14, CacheRead: .175}, false},
		{"gpt-5.1", Rates{Input: 1.25, Output: 10, CacheRead: .125}, false},
		{"gpt-5", Rates{Input: 1.25, Output: 10, CacheRead: .125}, false},
		{"gpt-5-mini", Rates{Input: .25, Output: 2, CacheRead: .025}, false},
		{"gpt-5-nano", Rates{Input: .05, Output: .4, CacheRead: .005}, false},
		{"gpt-4.1", Rates{Input: 2, Output: 8, CacheRead: .5}, false},
		{"gpt-4.1-mini", Rates{Input: .4, Output: 1.6, CacheRead: .1}, false},
		{"o3", Rates{Input: 2, Output: 8, CacheRead: .5}, false},
	} {
		if sameSnapshot(model, p.id) {
			return p.r, p.long, true
		}
	}
	// GPT-5.6+ has separately billed cache writes. Do not guess those rates or
	// price incomplete older Chat Completions counters as though writes were free.
	return Rates{}, false, false
}

// Estimate returns the standard API price for one request. Input is already
// normalized to include cache reads and writes by Prism's Anthropic adapter.
func Estimate(model string, t Tokens) (float64, bool) {
	return EstimateForContext(model, t, t.Input)
}

// EstimateForContext prices totals from requests within one context band.
// requestInput is a representative per-request input count, not the sum of
// several requests: GPT-5.4/5.5 long-context pricing starts above 272000 tokens.
func EstimateForContext(model string, t Tokens, requestInput int64) (float64, bool) {
	if r, longContext, ok := openAIRates(rawModel(model)); ok {
		if t.PricingProvider != "openai" || t.ServiceTier != "default" || !t.CacheReadKnown ||
			t.CacheWritePricingUnknown || t.Input < 0 || t.Output < 0 || t.CacheRead < 0 || t.CacheRead > t.Input ||
			t.CacheWrite != 0 || t.CacheWrite5m != 0 || t.CacheWrite1h != 0 || requestInput < 0 {
			return 0, false
		}
		if longContext && requestInput > 272000 {
			r.Input *= 2
			r.CacheRead *= 2
			r.Output *= 1.5
		}
		return (float64(t.Input-t.CacheRead)*r.Input + float64(t.CacheRead)*r.CacheRead + float64(t.Output)*r.Output) / 1_000_000, true
	}
	r, ok := RatesFor(model)
	uncached := t.Input - t.CacheRead - t.CacheWrite
	if !ok || t.CacheWritePricingUnknown || uncached < 0 || t.Output < 0 || t.CacheRead < 0 || t.CacheWrite < 0 || t.CacheWrite5m < 0 || t.CacheWrite1h < 0 {
		return 0, false
	}
	// Older records only contain the aggregate; the old client requested the
	// default five-minute TTL. New records with a breakdown must reconcile.
	fiveMinute := t.CacheWrite
	if t.CacheWrite5m != 0 || t.CacheWrite1h != 0 {
		if t.CacheWrite1h > t.CacheWrite || t.CacheWrite5m != t.CacheWrite-t.CacheWrite1h {
			return 0, false
		}
		fiveMinute = t.CacheWrite5m
	}
	return (float64(uncached)*r.Input + float64(t.CacheRead)*r.CacheRead +
		float64(fiveMinute)*r.CacheWrite + float64(t.CacheWrite1h)*r.Input*2 + float64(t.Output)*r.Output) / 1_000_000, true
}
