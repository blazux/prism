package usagecost

import (
	"math"
	"testing"
)

func TestEstimateCacheWriteTiers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tokens Tokens
		want   float64
		known  bool
	}{
		{name: "legacy five minute", tokens: Tokens{Input: 1000, Output: 10, CacheRead: 200, CacheWrite: 100}, want: 0.004475, known: true},
		{name: "mixed TTL", tokens: Tokens{Input: 1000, Output: 20, CacheRead: 100, CacheWrite: 300, CacheWrite5m: 100, CacheWrite1h: 200}, want: 0.006175, known: true},
		{name: "one hour only", tokens: Tokens{Input: 500, CacheWrite: 500, CacheWrite1h: 500}, want: 0.005, known: true},
		{name: "explicit zero", tokens: Tokens{}, known: true},
		{name: "unknown TTL", tokens: Tokens{Input: 1000, CacheWrite: 300, CacheWrite5m: 100, CacheWritePricingUnknown: true}},
		{name: "incomplete breakdown", tokens: Tokens{Input: 1000, CacheWrite: 300, CacheWrite1h: 100}},
		{name: "overlapping breakdown", tokens: Tokens{Input: 1000, CacheWrite: 300, CacheWrite5m: 200, CacheWrite1h: 200}},
		{name: "negative tier", tokens: Tokens{Input: 1000, CacheWrite: 300, CacheWrite5m: -1, CacheWrite1h: 301}},
		{name: "cache exceeds input", tokens: Tokens{Input: 100, CacheWrite: 101}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := Estimate("claude-opus-4-6", tc.tokens)
			if known != tc.known || math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("estimate = %.12f, %v; want %.12f, %v", got, known, tc.want, tc.known)
			}
		})
	}
}

func TestEstimateCachedOpus(t *testing.T) {
	got, ok := Estimate("claude-opus-4-6", Tokens{Input: 21440, Output: 5, CacheRead: 19676, CacheWrite: 1761})
	want := (3*5 + 19676*0.5 + 1761*6.25 + 5*25) / 1_000_000.0
	if !ok || got != want {
		t.Fatalf("cost = %f, %v; want %f", got, ok, want)
	}
}

func TestEstimateSourcePrefixedModel(t *testing.T) {
	for _, model := range []string{"anthropic::claude-sonnet-5", "anthropic::claude-sonnet-5-5"} {
		if _, ok := Estimate(model, Tokens{Input: 1000, Output: 100}); !ok {
			t.Fatalf("source-prefixed Claude model %s should have a price", model)
		}
	}
}

func TestEstimateUnknownOrInvalid(t *testing.T) {
	for _, tc := range []struct {
		model  string
		tokens Tokens
	}{
		{"custom-gateway", Tokens{Input: 100}},
		{"claude-opus-4-6", Tokens{Input: 2, CacheRead: 3}},
	} {
		if _, ok := Estimate(tc.model, tc.tokens); ok {
			t.Fatalf("unexpected estimate for %q %+v", tc.model, tc.tokens)
		}
	}
}

func openAITokens(input, output, cached int64) Tokens {
	return Tokens{Input: input, Output: output, CacheRead: cached,
		PricingProvider: "openai", ServiceTier: "default", CacheReadKnown: true}
}

func TestEstimateOpenAIStandardRates(t *testing.T) {
	for _, tc := range []struct {
		model                 string
		input, output, cached float64
	}{
		{"gpt-5.5", 5, 30, .5},
		{"gpt-5.5-2026-04-23", 5, 30, .5},
		{"source::gpt-5.4", 2.5, 15, .25},
		{"gpt-5.4-mini", .75, 4.5, .075},
		{"gpt-5.4-nano", .2, 1.25, .02},
		{"gpt-5.2", 1.75, 14, .175},
		{"gpt-5.1", 1.25, 10, .125},
		{"gpt-5", 1.25, 10, .125},
		{"gpt-5-mini", .25, 2, .025},
		{"gpt-5-nano", .05, .4, .005},
		{"gpt-4.1", 2, 8, .5},
		{"gpt-4.1-mini", .4, 1.6, .1},
		{"o3", 2, 8, .5},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got, known := Estimate(tc.model, openAITokens(1000, 100, 500))
			want := (500*tc.input + 100*tc.output + 500*tc.cached) / 1e6
			if !known || math.Abs(got-want) > 1e-12 {
				t.Fatalf("estimate %.12f, %v; want %.12f", got, known, want)
			}
		})
	}
}

func TestEstimateOpenAIRejectsUnknownPricing(t *testing.T) {
	for _, model := range []string{"gpt-5.6-sol", "gpt-6.2-sol", "gpt-5.5-pro", "gpt-5.4-chat-latest", "gpt-5.5-local", "gpt-5.5-2026-02-30", "gpt-5.5-2099-01-01", "gpt-5.50", "ft:gpt-5.5"} {
		if _, known := Estimate(model, openAITokens(1000, 100, 500)); known {
			t.Errorf("unexpected price for %s", model)
		}
	}
	for _, mutate := range []func(*Tokens){
		func(v *Tokens) { v.PricingProvider = "" },
		func(v *Tokens) { v.PricingProvider = "compatible" },
		func(v *Tokens) { v.ServiceTier = "" },
		func(v *Tokens) { v.ServiceTier = "priority" },
		func(v *Tokens) { v.ServiceTier = "flex" },
		func(v *Tokens) { v.CacheReadKnown = false },
		func(v *Tokens) { v.CacheRead = 1001 },
		func(v *Tokens) { v.CacheWrite = 10 },
		func(v *Tokens) { v.CacheWrite5m = 10 },
		func(v *Tokens) { v.CacheWritePricingUnknown = true },
	} {
		v := openAITokens(1000, 100, 500)
		mutate(&v)
		if _, known := Estimate("gpt-5.5", v); known {
			t.Errorf("unexpected price for incomplete/unsupported usage %+v", v)
		}
	}
}

func TestEstimateOpenAIContextBoundaryAndAggregate(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-5.4"} {
		r, _ := RatesFor(model)
		for _, input := range []int64{272000, 272001} {
			v := openAITokens(input, 100, 100000)
			got, known := Estimate(model, v)
			in, out, read := r.Input, r.Output, r.CacheRead
			if input > 272000 {
				in *= 2
				read *= 2
				out *= 1.5
			}
			want := (float64(input-100000)*in + 100*out + 100000*read) / 1e6
			if !known || math.Abs(got-want) > 1e-12 {
				t.Fatalf("%s %d: %.12f %v; want %.12f", model, input, got, known, want)
			}
		}
		// Two short requests total >272K; their sum still uses the short tariff.
		single, _ := Estimate(model, openAITokens(200000, 50, 100000))
		aggregate, known := EstimateForContext(model, openAITokens(400000, 100, 200000), 200000)
		if !known || math.Abs(aggregate-2*single) > 1e-12 {
			t.Fatalf("%s short requests charged as one long prompt: %.12f", model, aggregate)
		}
	}
}
