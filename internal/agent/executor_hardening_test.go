package agent

import (
	"strings"
	"testing"
)

func TestCanonicalToolName(t *testing.T) {
	cases := map[string]string{
		"docker_exec":    "docker_manage",
		"docker_stop":    "docker_manage",
		"cron_add":       "cron",
		"rag_delete":     "rag_manage",
		"delete_secret":  "secrets",
		"mcp_add_server": "mcp",
		"apt_install":    "install_packages",
		// Non-aliases pass through unchanged.
		"docker_manage": "docker_manage",
		"exec_command":  "exec_command",
		"wget":          "wget",
	}
	for in, want := range cases {
		if got := canonicalToolName(in); got != want {
			t.Errorf("canonicalToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCapToolResult(t *testing.T) {
	if got := capToolResult("short"); got != "short" {
		t.Errorf("small result must pass through unchanged, got %q", got)
	}
	// A result at the self-truncation size (8000) is well under the net.
	mid := strings.Repeat("x", 8000)
	if capToolResult(mid) != mid {
		t.Error("8000-char result should be untouched by the 24000 net")
	}
	big := strings.Repeat("a", 20000) + strings.Repeat("b", 20000)
	out := capToolResult(big)
	if len(out) >= len(big) {
		t.Fatalf("large result not capped: %d", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Error("cap must tell the model the result was truncated")
	}
	// Head and tail are both preserved so the model sees the shape.
	if !strings.HasPrefix(out, "aaaa") || !strings.HasSuffix(out, "bbbb") {
		t.Error("cap must keep head and tail")
	}
}

// A programmatic caller (/api/builtin → prismTool, cron) consumes results as
// data: the model-context cap must not touch them.
func TestCapResult_RawCallerUntouched(t *testing.T) {
	big := strings.Repeat("x", maxToolResultBytes*3)
	if got := (&ToolExecutor{}).capResult(big); len(got) >= len(big) {
		t.Error("model caller: huge result must be capped")
	}
	e := &ToolExecutor{}
	e.SetRawResults(true)
	if got := e.capResult(big); got != big {
		t.Errorf("raw caller: result must pass through untouched (got %d of %d bytes)", len(got), len(big))
	}
}
