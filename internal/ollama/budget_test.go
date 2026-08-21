package ollama

import "testing"

// ContextBudgetChars must derive a history budget that leaves the generation +
// system-prompt + tools reserve inside NumCtx — the whole point of the fix that
// stopped long thinking-model sessions returning empty. It must also never
// collapse below a usable floor for a tiny NumCtx.
func TestContextBudgetChars(t *testing.T) {
	orig := NumCtx
	defer func() { NumCtx = orig }()

	c := &Client{}

	NumCtx = 32768
	got := c.ContextBudgetChars()
	// (32768-18000)*3.5 ≈ 51688 — comfortably below the 150k default the agent
	// uses for large-context backends, so effectiveHistoryBudget picks this one.
	if got < 45000 || got > 60000 {
		t.Errorf("NumCtx=32768: budget=%d, want ~51k", got)
	}

	NumCtx = 8192 // the bad old default: reserve dominates, floor kicks in
	if floored := c.ContextBudgetChars(); floored != int(4000*charsPerToken) {
		t.Errorf("NumCtx=8192: budget=%d, want floor %d", floored, int(4000*charsPerToken))
	}
}
