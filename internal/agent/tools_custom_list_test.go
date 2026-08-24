package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/customtools"
)

// TestListToolsShowsSourcePath verifies list_tools surfaces an editable,
// workspace-relative source path for agent-created tools, and withholds it
// (labelling instead) for tools shipped with Prism — write_file has no
// overwrite guard, so a built-in's path must not invite clobbering.
func TestListToolsShowsSourcePath(t *testing.T) {
	// The dir basename is what listTools echoes as the workspace-relative prefix,
	// so name it "agent_tools" to mirror production (server.go).
	dir := filepath.Join(t.TempDir(), "agent_tools")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, header string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(header+"\nprint('ok')\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("my_thing.py", `# TOOL: {"name": "my_thing", "description": "does a thing"}`)
	write("pcap.py", `# TOOL: {"name": "pcap", "description": "reads a pcap", "protected": true}`)

	e := &ToolExecutor{}
	e.SetCustomTools(customtools.NewManager(dir), nil)

	out, err := e.listTools()
	if err != nil {
		t.Fatal(err)
	}

	// Agent-created tool: editable source path present.
	if !strings.Contains(out, "source: agent_tools/my_thing.py") {
		t.Errorf("expected editable source path for my_thing, got:\n%s", out)
	}
	// Shipped tool: labelled, and NO source path handed out.
	if !strings.Contains(out, "shipped with Prism") {
		t.Errorf("expected pcap to be labelled shipped, got:\n%s", out)
	}
	if strings.Contains(out, "source: agent_tools/pcap.py") {
		t.Errorf("must NOT expose an editable path for a shipped tool, got:\n%s", out)
	}
}

// TestRegisterToolReturnsSourcePath verifies register_tool tells the agent where
// the new tool's source lives, so it can edit it without a follow-up list_tools.
func TestRegisterToolReturnsSourcePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent_tools")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	e := &ToolExecutor{}
	e.SetCustomTools(customtools.NewManager(dir), nil)

	msg, err := e.registerTool("# TOOL: {\"name\": \"my_tool\", \"description\": \"d\"}\nprint('ok')\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Source: agent_tools/my_tool.py") {
		t.Errorf("expected source path in register message, got: %s", msg)
	}
}
