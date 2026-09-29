package usagecost

import "testing"

func TestEstimateCachedOpus(t *testing.T) {
	got, ok := Estimate("claude-opus-4-6", Tokens{Input: 21440, Output: 5, CacheRead: 19676, CacheWrite: 1761})
	want := (3*5 + 19676*0.5 + 1761*6.25 + 5*25) / 1_000_000.0
	if !ok || got != want {
		t.Fatalf("cost = %f, %v; want %f", got, ok, want)
	}
}

func TestEstimateSourcePrefixedModel(t *testing.T) {
	if _, ok := Estimate("anthropic::claude-sonnet-5", Tokens{Input: 1000, Output: 100}); !ok {
		t.Fatal("source-prefixed Claude model should have a price")
	}
}

func TestEstimateUnknownOrInvalid(t *testing.T) {
	for _, tc := range []struct {
		model  string
		tokens Tokens
	}{
		{"claude-sonnet-5-5", Tokens{Input: 100}},
		{"custom-gateway", Tokens{Input: 100}},
		{"claude-opus-4-6", Tokens{Input: 2, CacheRead: 3}},
	} {
		if _, ok := Estimate(tc.model, tc.tokens); ok {
			t.Fatalf("unexpected estimate for %q %+v", tc.model, tc.tokens)
		}
	}
}
