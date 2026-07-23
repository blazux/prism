package customtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
