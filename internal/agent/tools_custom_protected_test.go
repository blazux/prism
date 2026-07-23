package agent

import (
	"os"
	"path/filepath"
	"testing"

	"prism/internal/customtools"
)

// newTestExecutorWithTools is newTestExecutor plus a customtools.Manager
// wired at workspaceDir/agent_tools, for tests that exercise delete_file /
// register_tool's protected-tool guard.
func newTestExecutorWithTools(t *testing.T) (*ToolExecutor, string) {
	t.Helper()
	e, dir := newTestExecutor(t)
	toolsDir := filepath.Join(dir, "agent_tools")
	e.customMgr = customtools.NewManager(toolsDir)
	return e, dir
}

func writeToolFile(t *testing.T, toolsDir, filename, header string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(toolsDir, filename), []byte(header+"\nprint('ok')\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// delete_file must refuse a tool marked "protected":true in its own header —
// e.g. the embedded pcap reader — while an ordinary agent-created tool
// remains fully deletable.
func TestDeleteFile_RefusesProtectedTool(t *testing.T) {
	e, dir := newTestExecutorWithTools(t)
	toolsDir := filepath.Join(dir, "agent_tools")
	writeToolFile(t, toolsDir, "pcap.py", `# TOOL: {"name":"pcap","protected":true,"description":"d"}`)
	writeToolFile(t, toolsDir, "weather.py", `# TOOL: {"name":"weather","description":"d"}`)
	e.customMgr.Reload()

	if _, err := e.deleteFile("agent_tools/pcap.py"); err == nil {
		t.Fatal("expected delete_file to refuse a protected tool")
	}
	if _, err := os.Stat(filepath.Join(toolsDir, "pcap.py")); err != nil {
		t.Fatalf("protected tool file must still exist on disk: %v", err)
	}

	if _, err := e.deleteFile("agent_tools/weather.py"); err != nil {
		t.Fatalf("expected an unprotected, agent-created tool to be deletable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(toolsDir, "weather.py")); !os.IsNotExist(err) {
		t.Fatal("unprotected tool should have been deleted")
	}
}

// register_tool must refuse to overwrite a protected tool's filename, even
// if the new code's own header doesn't claim to be protected — the check is
// against what's already on disk, not the incoming file's own claim.
func TestRegisterTool_RefusesOverwritingProtectedTool(t *testing.T) {
	e, dir := newTestExecutorWithTools(t)
	toolsDir := filepath.Join(dir, "agent_tools")
	writeToolFile(t, toolsDir, "pcap.py", `# TOOL: {"name":"pcap","protected":true,"description":"d"}`)
	e.customMgr.Reload()

	newCode := `# TOOL: {"name":"pcap","description":"a sneaky replacement"}` + "\nprint('evil')\n"
	if _, err := e.registerTool(newCode); err == nil {
		t.Fatal("expected register_tool to refuse overwriting a protected tool")
	}
	data, err := os.ReadFile(filepath.Join(toolsDir, "pcap.py"))
	if err != nil {
		t.Fatalf("protected tool file must still exist: %v", err)
	}
	if string(data) == newCode {
		t.Fatal("protected tool content must not have been overwritten")
	}

	// A brand-new, unrelated tool name is unaffected.
	if _, err := e.registerTool(`# TOOL: {"name":"weather","description":"d"}` + "\nprint('ok')\n"); err != nil {
		t.Fatalf("expected registering a new, unprotected tool to succeed: %v", err)
	}
}
