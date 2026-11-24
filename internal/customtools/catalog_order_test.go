package customtools

import (
	"encoding/json"
	"reflect"
	"testing"

	"prism/internal/ollama"
)

func catalogOrderTool(name, description, filename string) Tool {
	return Tool{Name: name, Description: description, Filename: filename,
		Parameters: ollama.ToolParameters{Type: "object", Properties: map[string]ollama.ToolProperty{
			"value": {Type: "string", Description: description, Enum: []string{"one", "two"}},
		}, Required: []string{"value"}}}
}

func TestCustomCatalogOrderIgnoresDiscoveryOrder(t *testing.T) {
	a := catalogOrderTool("alpha", "Read alpha.", "a.py")
	b := catalogOrderTool("beta", "Read beta.", "b.py")
	c := catalogOrderTool("gamma", "Read gamma.", "c.py")
	var want string
	for _, tools := range [][]Tool{{a, b, c}, {c, a, b}, {b, c, a}, {c, b, a}} {
		m := NewManager(t.TempDir())
		// Inject an unchanged discovery snapshot; the empty fixture directory
		// keeps refreshIfStale from replacing it during these export checks.
		m.tools = append([]Tool(nil), tools...)
		got, err := json.Marshal(m.ToOllamaTools())
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			want = string(got)
		} else if string(got) != want {
			t.Fatal("unchanged tool schemas produced different provider catalogs")
		}
		if !reflect.DeepEqual(m.All(), tools) {
			t.Fatal("catalog export reordered the dispatch snapshot")
		}
	}
}

func TestCustomCatalogSortKeepsFirstDuplicateDispatch(t *testing.T) {
	first := catalogOrderTool("duplicate", "First script wins.", "z-first.py")
	second := catalogOrderTool("duplicate", "Second script must stay hidden.", "a-second.py")
	alpha := catalogOrderTool("alpha", "Separate tool.", "alpha.py")
	for _, discovery := range [][]Tool{{first, alpha, second}, {second, alpha, first}} {
		m := NewManager(t.TempDir())
		m.tools = append([]Tool(nil), discovery...)
		before := m.Get("duplicate")
		tools := m.ToOllamaTools()
		if len(tools) != 2 || tools[0].Function.Name != "alpha" || tools[1].Function.Name != "duplicate" {
			t.Fatalf("catalog was not sorted and deduplicated: %+v", tools)
		}
		if tools[1].Function.Description != before.llmDescription() || !reflect.DeepEqual(tools[1].Function.Parameters, before.Parameters) {
			t.Fatal("sorting selected a different duplicate's schema")
		}
		if !reflect.DeepEqual(m.Get("duplicate"), before) || !reflect.DeepEqual(m.All(), discovery) {
			t.Fatal("export changed which script execution would select")
		}
	}
}
