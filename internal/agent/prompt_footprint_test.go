package agent

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// Offline diagnostic of the real prompt builder and native tool catalog.
// No backend, database, workspace files, user data or network access is used.
// Sizes are bytes / Unicode characters, NOT provider token or billing counts.
func TestPromptFootprint(t *testing.T) {
	type size struct {
		Name              string
		Bytes, Characters int
	}
	measure := func(name, text string) size { return size{name, len(text), utf8.RuneCountInString(text)} }
	for _, name := range []string{"guided", "standard", "minimal"} {
		lean := name != "guided"
		a := &Agent{executor: &ToolExecutor{}, sessionID: "prompt-audit", limits: Limits{PromptProfile: name}}
		prompt := a.buildSystemPrompt(t.Context(), "")
		tools := a.buildToolList()
		raw, err := json.Marshal(tools)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: system=%+v tools=%d catalog=%+v combined_bytes=%d", name, measure("system", prompt), len(tools), measure("tools JSON", string(raw)), len(prompt)+len(raw))
		var sections []size
		// Include all text (including introductory content) exactly once.
		offset := 0
		title := "preamble"
		for _, line := range strings.SplitAfter(prompt, "\n") {
			if strings.HasPrefix(line, "## ") {
				if offset > 0 {
					sections = append(sections, measure(title, prompt[:offset]))
					prompt = prompt[offset:]
					offset = 0
				}
				title = strings.TrimSpace(line)
			}
			offset += len(line)
		}
		sections = append(sections, measure(title, prompt))
		sort.Slice(sections, func(i, j int) bool { return sections[i].Bytes > sections[j].Bytes })
		for _, s := range sections {
			t.Logf("%s section: %+v", name, s)
		}
		if !lean {
			var sizes []size
			for _, tool := range tools {
				b, err := json.Marshal(tool)
				if err != nil {
					t.Fatal(err)
				}
				sizes = append(sizes, measure(tool.Function.Name, string(b)))
			}
			sort.Slice(sizes, func(i, j int) bool { return sizes[i].Bytes > sizes[j].Bytes })
			for i, s := range sizes {
				if i == 15 {
					break
				}
				t.Logf("largest tool: %+v", s)
			}
		}
	}
}
