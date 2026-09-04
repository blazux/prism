package agent

import (
	"strings"
	"testing"
)

// Every lean strip/rewrite must still match the guided text (else it silently
// stops applying), the lean rendering must not contain them, and the lean
// profile must be substantially shorter while keeping the product contracts
// and safety rules.
func TestLeanProfile(t *testing.T) {
	guided := systemPromptCore + systemPromptCoreTail
	for _, p := range leanStrips {
		if strings.Count(guided, p) != 1 {
			t.Errorf("lean strip no longer matches the guided prompt exactly once: %.80q…", p)
		}
	}
	for _, r := range leanRewrites {
		if strings.Count(guided, r.old) != 1 {
			t.Errorf("lean rewrite no longer matches the guided prompt exactly once: %.80q…", r.old)
		}
	}
	lean := systemPromptCoreFor(true) + systemPromptCoreTailFor(true)
	for _, p := range leanStrips {
		if strings.Contains(lean, p) {
			t.Errorf("lean prompt still contains a stripped passage: %.60q…", p)
		}
	}
	for _, must := range []string{"prismTool(", "prismChat(", "## Destructive actions", "## Pause before heavy", "/api/builtin/", "## Helping the user with Prism itself", "Never hardcode hex colors"} {
		if !strings.Contains(lean, must) {
			t.Errorf("lean prompt lost a product/safety passage: %q", must)
		}
	}
	g, l := len(guided)+len(systemPromptRetryGuided)+len(systemPromptActTurn), len(lean)+len(systemPromptRetryLean)+len(systemPromptKeepItSimple)
	t.Logf("guided %d chars (~%d tok) · lean %d chars (~%d tok) · lean/guided = %.0f%%", g, g/4, l, l/4, 100*float64(l)/float64(g))
	if float64(l) > 0.8*float64(g) {
		t.Errorf("lean profile is not meaningfully lighter: %d vs %d", l, g)
	}
	if strings.Contains(systemPromptKeepItSimple, "`") {
		t.Error("systemPromptKeepItSimple must not contain backticks (raw string)")
	}
}
