package customtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLLMDescriptionComposition(t *testing.T) {
	// Base only → unchanged.
	base := Tool{Description: "Fetch a stock quote."}
	if got := base.llmDescription(); got != "Fetch a stock quote." {
		t.Errorf("base = %q", got)
	}
	// With when_to_use + usage → folded in.
	rich := Tool{
		Description: "Fetch a stock quote.",
		WhenToUse:   "the user asks for a share price",
		Usage:       "pass {\"ticker\":\"AAPL\"}",
	}
	got := rich.llmDescription()
	for _, want := range []string{"Fetch a stock quote.", "When to use: the user asks for a share price", "Usage: pass {\"ticker\":\"AAPL\"}"} {
		if !strings.Contains(got, want) {
			t.Errorf("composed description missing %q; got %q", want, got)
		}
	}
}

func TestParseToolMetaRichFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quote.py")
	script := "# TOOL: {\"name\":\"quote\",\"description\":\"Fetch a quote.\",\"when_to_use\":\"share price asked\",\"usage\":\"pass a ticker\",\"parameters\":{\"type\":\"object\"}}\nprint('ok')\n"
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	tool, ok := parseToolMeta(path, "quote.py")
	if !ok {
		t.Fatal("parseToolMeta returned !ok")
	}
	if tool.WhenToUse != "share price asked" || tool.Usage != "pass a ticker" {
		t.Errorf("rich fields not parsed: when=%q usage=%q", tool.WhenToUse, tool.Usage)
	}
	if tool.Protected {
		t.Error("a tool with no \"protected\" key must default to false")
	}
}

func TestParseToolMetaProtected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pcap.py")
	script := "# TOOL: {\"name\":\"pcap\",\"protected\":true,\"description\":\"Decode a capture.\"}\nprint('ok')\n"
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	tool, ok := parseToolMeta(path, "pcap.py")
	if !ok {
		t.Fatal("parseToolMeta returned !ok")
	}
	if !tool.Protected {
		t.Error("expected Protected=true from \"protected\":true in the header")
	}
}

func TestIsProtectedFilename(t *testing.T) {
	dir := t.TempDir()
	write := func(filename, script string) {
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(script), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("pcap.py", "# TOOL: {\"name\":\"pcap\",\"protected\":true,\"description\":\"d\"}\n")
	write("weather.py", "# TOOL: {\"name\":\"weather\",\"description\":\"d\"}\n")

	m := NewManager(dir)
	if !m.IsProtectedFilename("pcap.py") {
		t.Error("pcap.py should be protected")
	}
	if m.IsProtectedFilename("weather.py") {
		t.Error("weather.py (agent-created, no protected flag) must not be protected")
	}
	if m.IsProtectedFilename("does_not_exist.py") {
		t.Error("an unknown filename must not be reported as protected")
	}
}

// A tool file removed any way other than Prism's own write_file/delete_file/
// register_tool (a shell `rm`, editing the host filesystem directly) used to
// leave a stale entry in every read (All/Get/ToOllamaTools) until something
// unrelated happened to call Reload — the model kept seeing and calling a
// tool whose script no longer existed. All four read methods now self-heal
// via refreshIfStale without any explicit Reload call.
func TestOutOfBandDeletionIsPickedUpWithoutExplicitReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weather.py")
	if err := os.WriteFile(path, []byte("# TOOL: {\"name\":\"weather\",\"description\":\"d\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	if m.Get("weather") == nil {
		t.Fatal("expected weather tool to be discovered on construction")
	}

	// Some filesystems (tmpfs observed here) batch mtime updates into coarse
	// ticks — the write above and the remove below could otherwise land in
	// the same tick and produce an identical mtime, defeating the check this
	// test exists to exercise. Sleep BEFORE the removal, not after, so it
	// falls in a later tick than the write NewManager already captured.
	time.Sleep(20 * time.Millisecond)

	// Remove it exactly as an out-of-band `rm` would — no call to Reload().
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if tool := m.Get("weather"); tool != nil {
		t.Error("Get should no longer see a tool whose file was removed out-of-band")
	}
	if got := m.All(); len(got) != 0 {
		t.Errorf("All() = %v, want empty after out-of-band deletion", got)
	}
	if got := m.ToOllamaTools(); len(got) != 0 {
		t.Errorf("ToOllamaTools() = %v, want empty after out-of-band deletion", got)
	}
	if m.IsProtectedFilename("weather.py") {
		t.Error("a removed file must not be reported as protected")
	}
}
