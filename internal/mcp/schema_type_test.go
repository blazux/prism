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

// TestToOllamaTool_EnumPassthrough ensures a constrained parameter's allowed
// values reach the model instead of being dropped to a bare "string" it must
// guess at.
func TestToOllamaTool_EnumPassthrough(t *testing.T) {
	tool := Tool{
		Name: "set_status",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"status":{"type":"string","enum":["open","closed","pending"]},
				"priority":{"type":"integer","enum":[1,2,3]},
				"note":{"type":"string"}
			}
		}`),
	}
	props := toOllamaTool(tool).Function.Parameters.Properties

	if got := props["status"].Enum; len(got) != 3 || got[0] != "open" || got[2] != "pending" {
		t.Errorf("status enum = %v, want [open closed pending]", got)
	}
	if got := props["priority"].Enum; len(got) != 3 || got[0] != "1" || got[2] != "3" {
		t.Errorf("priority enum (non-string) = %v, want [1 2 3] stringified", got)
	}
	if got := props["note"].Enum; got != nil {
		t.Errorf("unconstrained param should have no enum, got %v", got)
	}
}

// TestToOllamaTool_ArrayItems ensures an array param's element type reaches the
// model instead of arriving as a bare "array" it must guess the shape of.
func TestToOllamaTool_ArrayItems(t *testing.T) {
	tool := Tool{
		Name: "run",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"ports":{"type":"array","items":{"type":"integer"}},
				"actions":{"type":"array","items":{"type":"object"}},
				"tags":{"type":"array","items":{"type":"string","enum":["a","b"]}}
			}
		}`),
	}
	props := toOllamaTool(tool).Function.Parameters.Properties

	if it := props["ports"].Items; it == nil || it.Type != "integer" {
		t.Errorf("ports.items type = %v, want integer", props["ports"].Items)
	}
	if it := props["actions"].Items; it == nil || it.Type != "object" {
		t.Errorf("actions.items type = %v, want object", props["actions"].Items)
	}
	if it := props["tags"].Items; it == nil || len(it.Enum) != 2 || it.Enum[0] != "a" {
		t.Errorf("tags.items enum = %v, want [a b]", props["tags"].Items)
	}
}
