package mcp

import (
	"encoding/json"
	"testing"
)

// TestResolveSchemaType covers the shapes MCP servers emit for a parameter's
// type — including the Optional[str] forms (FastMCP) that used to leak an empty
// type string to the backend and 400 strict servers like llama.cpp (Qwopus).
func TestResolveSchemaType(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain string", `{"type":"string"}`, "string"},
		{"integer", `{"type":"integer"}`, "integer"},
		{"array union string|null", `{"type":["string","null"],"description":"opt"}`, "string"},
		{"array null|integer", `{"type":["null","integer"]}`, "integer"},
		{"anyOf string|null", `{"anyOf":[{"type":"string"},{"type":"null"}],"description":"opt"}`, "string"},
		{"oneOf null|number", `{"oneOf":[{"type":"null"},{"type":"number"}]}`, "number"},
		{"no type at all", `{"description":"mystery"}`, "string"},
		{"malformed", `not json`, "string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveSchemaType(json.RawMessage(c.raw)); got != c.want {
				t.Fatalf("resolveSchemaType(%s) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// TestToOllamaTool_OptionalParamHasType is the regression for the Broadworks
// list_enterprises(cluster_name: Optional[str]) 400: every property that reaches
// the backend must carry a non-empty type.
func TestToOllamaTool_OptionalParamHasType(t *testing.T) {
	tool := Tool{
		Name:        "list_enterprises",
		Description: "List enterprises",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"cluster_name":{"anyOf":[{"type":"string"},{"type":"null"}],"description":"Optional cluster name to search. If None, searches all clusters."}
			}
		}`),
	}
	got := toOllamaTool(tool)
	prop, ok := got.Function.Parameters.Properties["cluster_name"]
	if !ok {
		t.Fatal("cluster_name property missing")
	}
	if prop.Type == "" {
		t.Fatalf("cluster_name has empty type — would 400 llama.cpp")
	}
	if prop.Type != "string" {
		t.Fatalf("cluster_name type = %q, want string", prop.Type)
	}
}
