// Package usagecost estimates first-party Claude API chat-loop spend from
// provider-reported token counts. It is diagnostic, not an invoice: auxiliary
// calls, negotiated discounts and future price changes are outside its scope.
package usagecost

import "strings"

// Prices checked against https://platform.claude.com/docs/en/models/ on
// 2026-09-29. All rates are USD per million tokens, for 5-minute cache writes.
const PricingAsOf = "2026-09-29"

type Rates struct {
	Input, Output, CacheWrite, CacheRead float64
}

type Tokens struct {
	Input, Output, CacheRead, CacheWrite int64
}

func RatesFor(model string) (Rates, bool) {
	model = strings.TrimPrefix(model, "anthropic::")
	for _, p := range []struct {
		id string
		r  Rates
	}{
		{"claude-opus-4-6", Rates{5, 25, 6.25, 0.50}},
		{"claude-sonnet-4-6", Rates{3, 15, 3.75, 0.30}},
		{"claude-sonnet-5-5", Rates{2, 10, 2.50, 0.20}},
		{"claude-sonnet-5", Rates{2, 10, 2.50, 0.20}},
	} {
		// A dated snapshot is the same model; a different minor version isn't.
		if model == p.id || strings.HasPrefix(model, p.id+"-20") {
			return p.r, true
		}
	}
	return Rates{}, false
}

// Estimate returns the standard API price for one request. Input is already
// normalized to include cache reads and writes by Prism's Anthropic adapter.
func Estimate(model string, t Tokens) (float64, bool) {
	r, ok := RatesFor(model)
	uncached := t.Input - t.CacheRead - t.CacheWrite
	if !ok || uncached < 0 || t.Output < 0 || t.CacheRead < 0 || t.CacheWrite < 0 {
		return 0, false
	}
	return (float64(uncached)*r.Input + float64(t.CacheRead)*r.CacheRead +
		float64(t.CacheWrite)*r.CacheWrite + float64(t.Output)*r.Output) / 1_000_000, true
}
