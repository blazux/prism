package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestMinimalCatalogPreservesToolContracts(t *testing.T) {
	before, _ := json.Marshal(ToolDefinitions)
	standard := nativeToolsFor(true)
	minimal := minimalToolCatalog(standard)
	if len(minimal) != len(standard) {
		t.Fatal("minimal removed native tools")
	}
	known := map[string]ollama.Tool{}
	for _, tool := range standard {
		known[tool.Function.Name] = tool
	}
	for i, tool := range minimal {
		original := standard[i]
		if tool.Type != original.Type || tool.Function.Name != original.Function.Name ||
			tool.Function.Parameters.Type != original.Function.Parameters.Type ||
			!reflect.DeepEqual(tool.Function.Parameters.Required, original.Function.Parameters.Required) ||
			len(tool.Function.Parameters.Properties) != len(original.Function.Parameters.Properties) ||
			tool.Function.Description == "" {
			t.Fatalf("minimal changed tool contract: %s", tool.Function.Name)
		}
		for key, property := range tool.Function.Parameters.Properties {
			old, ok := original.Function.Parameters.Properties[key]
			if !ok || (old.Description != "" && property.Description == "") {
				t.Fatalf("missing property description: %s.%s", tool.Function.Name, key)
			}
			property.Description, old.Description = "", ""
			if !reflect.DeepEqual(property, old) {
				t.Fatalf("minimal changed schema of %s.%s", tool.Function.Name, key)
			}
		}
	}
	for name := range minimalToolDescriptions {
		if _, ok := known[name]; !ok {
			t.Errorf("unknown minimal tool: %s", name)
		}
	}
	for name, props := range minimalToolPropertyDescriptions {
		tool, ok := known[name]
		if !ok {
			t.Errorf("unknown minimal tool: %s", name)
			continue
		}
		for key := range props {
			if _, ok := tool.Function.Parameters.Properties[key]; !ok {
				t.Errorf("unknown minimal property: %s.%s", name, key)
			}
		}
	}
	standardRaw, _ := json.Marshal(standard)
	minimalRaw, _ := json.Marshal(minimal)
	if len(minimalRaw) >= len(standardRaw)*95/100 {
		t.Fatalf("minimal catalog saves <5%%: standard=%d minimal=%d", len(standardRaw), len(minimalRaw))
	}
	after, _ := json.Marshal(ToolDefinitions)
	if string(before) != string(after) {
		t.Fatal("minimal mutated shared definitions")
	}
}

func TestMinimalCatalogKeepsCriticalGuidanceAndDynamicTools(t *testing.T) {
	byName := map[string]ollama.Tool{}
	for _, tool := range minimalToolCatalog(nativeToolsFor(true)) {
		byName[tool.Function.Name] = tool
	}
	for _, check := range []struct{ tool, term string }{
		{"agent_settings", "reindex needs user consent"},
		{"email", "folder with UID"},
		{"cron", "not secret values"},
		{"cron", "$PRISM_SESSION"},
		{"widget", "screenshot+console errors"},
		{"widget", "remove only on request"},
		{"pim_source", "request_secret"},
	} {
		if !strings.Contains(byName[check.tool].Function.Description, check.term) {
			t.Errorf("%s lost %q", check.tool, check.term)
		}
	}
	dynamic := ollama.Tool{Type: "function", Function: ollama.ToolFunction{Name: "custom_fixture", Description: "Untouched provider contract"}}
	a := &Agent{executor: &ToolExecutor{telephonyTools: []ollama.Tool{dynamic}}, limits: Limits{PromptProfile: "minimal"}}
	tools := a.buildToolList()
	if !reflect.DeepEqual(tools[len(tools)-1], dynamic) {
		t.Fatal("minimal changed dynamic tool")
	}
}
