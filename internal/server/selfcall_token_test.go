package server

import (
	"os"
	"regexp"
	"testing"
)

// The agent's self-calls (exec_command, custom tools, cron, widget preview)
// must be handed a per-session capability token, not the deployment token —
// that is the whole of A2 (captoken.go). There are three ToolExecutor
// construction sites (ws, /api/chat, /api/builtin) and it is easy to fix two
// and miss the third (which is exactly what happened once). This scans the
// package so a new or reverted site that passes s.cfg.AuthToken fails the build.
func TestExecutorsUseCapabilityToken(t *testing.T) {
	files, _ := os.ReadDir(".")
	newExec := regexp.MustCompile(`NewToolExecutor\([^)]*\)`)
	authTok := regexp.MustCompile(`NewToolExecutor\([^)]*s\.cfg\.AuthToken\s*\)`)
	found := 0
	for _, f := range files {
		if f.IsDir() || !regexp.MustCompile(`\.go$`).MatchString(f.Name()) ||
			regexp.MustCompile(`_test\.go$`).MatchString(f.Name()) {
			continue
		}
		b, err := os.ReadFile(f.Name())
		if err != nil {
			continue
		}
		for _, call := range newExec.FindAllString(string(b), -1) {
			found++
			if authTok.MatchString(call) {
				t.Errorf("%s: a ToolExecutor is built with the deployment token instead of s.selfCallToken(sessionID) — the agent would get global-admin $PRISM_TOKEN: %s", f.Name(), call)
			}
		}
	}
	if found == 0 {
		t.Fatal("no NewToolExecutor call found — did the scan break?")
	}
}
