package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestLeanToolsPreserveContracts(t *testing.T) {
	before, _ := json.Marshal(ToolDefinitions)
	guided, lean := nativeToolsFor(false), nativeToolsFor(true)
	if len(guided) != len(lean) {
		t.Fatal("lean removed tools")
	}
	names := map[string]bool{}
	for i, tool := range guided {
		names[tool.Function.Name] = true
		if tool.Type != lean[i].Type || tool.Function.Name != lean[i].Function.Name || !reflect.DeepEqual(tool.Function.Parameters, lean[i].Function.Parameters) {
			t.Fatalf("changed tool schema: %s", tool.Function.Name)
		}
		if lean[i].Function.Description == "" {
			t.Fatalf("missing description: %s", tool.Function.Name)
		}
	}
	for name := range leanToolDescriptions {
		if !names[name] {
			t.Errorf("stale lean description: %s", name)
		}
	}
	raw, _ := json.Marshal(lean)
	if len(raw) >= len(before)*90/100 {
		t.Fatal("lean tool catalog should save at least 10% without changing schemas")
	}
	after, _ := json.Marshal(ToolDefinitions)
	if string(before) != string(after) || !reflect.DeepEqual(guided, ToolDefinitions) {
		t.Fatal("lean mutated guided catalog")
	}
}

func TestLeanToolSelectionPreservesDynamicAndDisabled(t *testing.T) {
	on, off := true, false
	// A provider-supplied tool sharing a native name must retain its own contract.
	dynamic := ollama.Tool{Type: "function", Function: ollama.ToolFunction{Name: "widget", Description: "Provider-owned contract"}}
	a := &Agent{executor: &ToolExecutor{telephonyTools: []ollama.Tool{dynamic}}, limits: Limits{LeanPrompt: &on}, disabledTools: []string{"cron"}}
	for _, lean := range []bool{true, false, true} {
		if lean {
			a.limitsOverride.LeanPrompt = &on
		} else {
			a.limitsOverride.LeanPrompt = &off
		}
		tools := a.buildToolList()
		if !reflect.DeepEqual(tools[len(tools)-1], dynamic) {
			t.Fatal("modified a dynamic tool")
		}
		for _, tool := range tools[:len(tools)-1] {
			if tool.Function.Name == "cron" {
				t.Fatal("disabled tool reappeared")
			}
			if tool.Function.Name == "widget" && (tool.Function.Description == leanToolDescriptions["widget"]) != lean {
				t.Fatal("wrong profile selected")
			}
		}
	}
}

func TestLeanCoreOperationalContracts(t *testing.T) {
	for _, contract := range []string{"prismTool(name, args)", "prismChat(message)", "prismNotify(", "prismSuggest(", "prismContext(", "prismOpenFile(", "prismOnData(", "window.PRISM_SESSION", "min-height:0", "never embed server tokens", "secrets/<name>?session=$PRISM_SESSION", "no injected theme", "/proxy/<published-port>/", "Docker tool-returned URLs", "inspect once"} {
		if !strings.Contains(systemPromptCoreFor(true), contract) {
			t.Errorf("missing lean contract: %s", contract)
		}
	}
	// Destructive-action and approval rules are retained verbatim from the shared tail.
	guided, lean := systemPromptCoreTailFor(false), systemPromptCoreTailFor(true)
	section := func(s string) string {
		s = s[strings.Index(s, destructiveHeading):]
		if end := strings.Index(s, "\n## "); end >= 0 {
			return s[:end]
		}
		return s
	}
	if section(guided) != section(lean) {
		t.Fatal("lean changed destructive action rules")
	}
}
