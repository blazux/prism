package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

func catalogOrderTool(name, description string) Tool {
	return Tool{Name: name, Description: description, InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)}
}

func TestMCPCatalogOrderIgnoresProviderDiscoveryOrder(t *testing.T) {
	a := catalogOrderTool("alpha", "Read alpha.")
	b := catalogOrderTool("beta", "Read beta.")
	c := catalogOrderTool("gamma", "Read gamma.")
	var want string
	for _, servers := range [][]ServerConfig{
		{{ID: "first", Enabled: true, Tools: []Tool{a, b}}, {ID: "second", Enabled: true, Tools: []Tool{c}}},
		{{ID: "first", Enabled: true, Tools: []Tool{b, a}}, {ID: "second", Enabled: true, Tools: []Tool{c}}},
		{{ID: "second", Enabled: true, Tools: []Tool{c}}, {ID: "first", Enabled: true, Tools: []Tool{b, a}}},
	} {
		before, _ := json.Marshal(servers)
		got, err := json.Marshal(toOllamaTools(servers))
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			want = string(got)
		} else if string(got) != want {
			t.Fatal("unchanged MCP schemas produced different provider catalogs")
		}
		after, _ := json.Marshal(servers)
		if string(before) != string(after) {
			t.Fatal("catalog export reordered the dispatch registry")
		}
	}
}

func TestMCPCatalogSortKeepsFirstEnabledDuplicate(t *testing.T) {
	first := catalogOrderTool("duplicate", "First enabled server wins.")
	first.InputSchema = json.RawMessage(`{"type":"object","properties":{"first":{"type":"boolean"}},"required":["first"]}`)
	second := catalogOrderTool("duplicate", "Second server must stay hidden.")
	alpha := catalogOrderTool("alpha", "Separate tool.")
	for _, tc := range []struct {
		servers []ServerConfig
		winner  Tool
	}{
		{[]ServerConfig{{ID: "z-first", Enabled: true, Tools: []Tool{first, alpha}}, {ID: "a-second", Enabled: true, Tools: []Tool{second}}}, first},
		{[]ServerConfig{{ID: "a-second", Enabled: true, Tools: []Tool{second, alpha}}, {ID: "z-first", Enabled: true, Tools: []Tool{first}}}, second},
		{[]ServerConfig{{ID: "disabled", Enabled: false, Tools: []Tool{second}}, {ID: "z-first", Enabled: true, Tools: []Tool{first, alpha}}}, first},
		{[]ServerConfig{{ID: "single", Enabled: true, Tools: []Tool{first, alpha, second}}}, first},
	} {
		tools := toOllamaTools(tc.servers)
		if len(tools) != 2 || tools[0].Function.Name != "alpha" || tools[1].Function.Name != "duplicate" {
			t.Fatalf("MCP catalog was not sorted and deduplicated: %+v", tools)
		}
		if !reflect.DeepEqual(tools[1], toOllamaTool(tc.winner)) {
			t.Fatal("sorting selected a different duplicate's schema than CallTool's traversal")
		}
	}
}
