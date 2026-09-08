package agent

import (
	"strings"
	"testing"
)

// The profile markers must be well-formed, neither rendering may leak one, and
// the lean profile must be substantially shorter while keeping the product
// contracts and safety rules.
func TestLeanProfile(t *testing.T) {
	raw := systemPromptCore + systemPromptCoreTail
	if strings.Count(raw, markGuided) != strings.Count(raw, markEnd) {
		t.Fatalf("unbalanced profile markers: %d %s vs %d %s", strings.Count(raw, markGuided), markGuided, strings.Count(raw, markEnd), markEnd)
	}
	for rest := raw; ; {
		i := strings.Index(rest, markGuided)
		if i < 0 {
			break
		}
		end := strings.Index(rest[i:], markEnd)
		if end < 0 {
			t.Fatalf("profile block never closed: %.80q…", rest[i:])
		}
		block := rest[i+len(markGuided) : i+end]
		if strings.Contains(block, markGuided) {
			t.Errorf("nested profile block: %.80q…", block)
		}
		if strings.Count(block, markLean) > 1 {
			t.Errorf("profile block with two lean branches: %.80q…", block)
		}
		rest = rest[i+end+len(markEnd):]
	}
	guided := systemPromptCoreFor(false) + systemPromptCoreTailFor(false)
	lean := systemPromptCoreFor(true) + systemPromptCoreTailFor(true)
	for _, out := range []string{guided, lean} {
		if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
			t.Errorf("rendered prompt still contains a marker: %.80q…", out[strings.Index(out, "{{"):])
		}
	}
	for _, must := range []string{"prismTool(", "prismChat(", "## Destructive actions", "## Pause before heavy", "/api/builtin/", "## Helping the user with Prism itself", "Never hardcode hex colors", "rm -rf /workspace"} {
		if !strings.Contains(lean, must) {
			t.Errorf("lean prompt lost a product/safety passage: %q", must)
		}
	}
	g, l := len(guided)+len(systemPromptRetryGuided)+len(systemPromptActTurn), len(lean)+len(systemPromptRetryLean)+len(systemPromptTurnContract)+len(systemPromptKeepItSimple)
	t.Logf("guided %d chars (~%d tok) · lean %d chars (~%d tok) · lean/guided = %.0f%%", g, g/4, l, l/4, 100*float64(l)/float64(g))
	if float64(l) > 0.8*float64(g) {
		t.Errorf("lean profile is not meaningfully lighter: %d vs %d", l, g)
	}
	if strings.Contains(systemPromptKeepItSimple, "`") {
		t.Error("systemPromptKeepItSimple must not contain backticks (raw string)")
	}
	// Tone: outside Destructive actions the lean profile does not shout; inside
	// it still does — capitals there mean data loss and nothing else.
	di := strings.Index(lean, destructiveHeading)
	if di < 0 {
		t.Fatal("lean prompt lost the Destructive actions section")
	}
	dEnd := di + len(destructiveHeading) + strings.Index(lean[di+len(destructiveHeading):], "\n## ")
	for _, part := range []string{lean[:di], lean[dEnd:]} {
		if m := shoutedRe.FindString(part); m != "" {
			t.Errorf("lean prompt still shouts %q outside Destructive actions", m)
		}
	}
	if !strings.Contains(lean, "\nAlways pass ?session=") {
		t.Error("a sentence-initial emphasis word must be re-capitalised in the source, not left lowercase by leanTone")
	}
	if !strings.Contains(lean[di:dEnd], "NEVER run `rm -rf /workspace`") {
		t.Error("Destructive actions lost its emphasis in the lean profile")
	}
	if !strings.Contains(guided, "do NOT append ?session=") || !strings.Contains(lean, "do not append ?session=") {
		t.Error("leanTone should lowercase emphasis in lean only")
	}
	// The harness fact survives the cut: a frontier model cannot guess that a
	// reply without a tool call ends the turn.
	if !strings.Contains(systemPromptTurnContract, "no tool call is the final answer") {
		t.Error("systemPromptTurnContract lost the turn-ends-here fact")
	}
}
