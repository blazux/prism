package agent

import (
	"context"
	"strings"
	"testing"
)

func TestCommitMessage(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "agent turn"},
		{"   ", "agent turn"},
		{"fix the widget", "turn: fix the widget"},
		{"first line\nsecond line", "turn: first line"},
		{"line one\r\nline two", "turn: line one"},
	}
	for _, c := range cases {
		if got := commitMessage(c.in); got != c.want {
			t.Errorf("commitMessage(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Long input is truncated to a bounded, rune-safe subject (no split runes —
	// the model emits multilingual text, so byte-slicing would corrupt it).
	long := strings.Repeat("é", 200) // 200 runes, 400 bytes
	got := commitMessage(long)
	if !strings.HasPrefix(got, "turn: ") || !strings.HasSuffix(got, "…") {
		t.Errorf("long message not prefixed/ellipsized: %q", got)
	}
	if !utf8ValidTrimmed(got) {
		t.Errorf("truncation split a rune: %q", got)
	}
}

func utf8ValidTrimmed(s string) bool {
	for _, r := range s {
		if r == '�' { // replacement char = an invalid byte sequence
			return false
		}
	}
	return true
}

// CommitWorkspace must be a safe no-op (never panic) when there is no workspace
// container to run git in — versioning is a best-effort safety net, so a missing
// or misconfigured docker manager just disables it silently.
func TestCommitWorkspace_NoDockerIsSafeNoop(t *testing.T) {
	var e *ToolExecutor // nil executor
	e.CommitWorkspace(context.Background(), "anything")

	e2 := &ToolExecutor{} // docker == nil
	e2.CommitWorkspace(context.Background(), "anything")
}
