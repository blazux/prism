package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"prism/internal/memory"
	"prism/internal/ollama"
)

func TestCatalogueFiltersRightsWithoutMutatingGlobalSchemas(t *testing.T) {
	before, _ := json.Marshal(ToolDefinitions)
	e := &ToolExecutor{multiUser: true, headless: true, sessionID: "u7-telegram", sharingGroups: []memory.Membership{{GroupID: 3}}, toolGuard: func(name string, args map[string]interface{}) error {
		if name == "exec_command" || name == "rag_manage" && args["action"] == "delete" {
			return errors.New("denied")
		}
		return nil
	}}
	a := &Agent{executor: e, limits: Limits{PromptProfile: "standard"}}
	tools := a.buildToolList()
	var settings, widget, ragTool *ollama.Tool
	for i := range tools {
		switch tools[i].Function.Name {
		case "exec_command", "editor", "request_secret":
			t.Fatal("unavailable capability exposed", tools[i].Function.Name)
		case "agent_settings":
			settings = &tools[i]
		case "widget":
			widget = &tools[i]
		case "rag_manage":
			ragTool = &tools[i]
		}
	}
	if settings == nil || widget == nil || ragTool == nil {
		t.Fatal("useful composite tool disappeared")
	}
	if slices.Contains(settings.Function.Parameters.Properties["action"].Enum, "ai_get") || len(settings.Function.Parameters.Properties) > 9 {
		t.Fatal("inaccessible AI settings sent to member")
	}
	if slices.Contains(widget.Function.Parameters.Properties["action"].Enum, "add") || slices.Contains(widget.Function.Parameters.Properties["action"].Enum, "add_shared") {
		t.Fatal("headless widget creation exposed")
	}
	if _, exists := widget.Function.Parameters.Properties["content"]; exists {
		t.Fatal("unused HTML schema still in headless context")
	}
	if slices.Contains(ragTool.Function.Parameters.Properties["action"].Enum, "delete") {
		t.Fatal("denied composite action exposed")
	}
	if err := e.Authorize("exec_command", json.RawMessage(`{}`)); err == nil {
		t.Fatal("catalogue filtering replaced execution authorization")
	}
	after, _ := json.Marshal(ToolDefinitions)
	if string(before) != string(after) {
		t.Fatal("global schema mutated")
	}
	// A later caller with broader rights gets the original actions and parameters.
	admin := &Agent{executor: &ToolExecutor{globalAdmin: true}, limits: Limits{PromptProfile: "standard"}}
	for _, tool := range admin.buildToolList() {
		if tool.Function.Name == "agent_settings" && !slices.Contains(tool.Function.Parameters.Properties["action"].Enum, "ai_get") {
			t.Fatal("member filtering leaked to admin")
		}
	}
	admin.executor.hiddenTools = map[string]bool{"exec_command": true}
	for _, tool := range admin.buildToolList() {
		if tool.Function.Name == "exec_command" {
			t.Fatal("full-access catalog exposed a globally disabled tool")
		}
	}
	full, _ := json.Marshal(nativeToolsFor(true))
	filtered, _ := json.Marshal(tools)
	t.Logf("standard native catalog: before=%d bytes, restricted headless=%d bytes, saved=%d bytes", len(full), len(filtered), len(full)-len(filtered))
	if len(filtered) >= len(full) {
		t.Fatal("catalogue failed to shrink")
	}
	// The executor still performs allowed ordinary work with the same primitives.
	e.workspaceDir = t.TempDir()
	if result, _, err := e.Execute(context.Background(), "write_file", json.RawMessage(`{"path":"data/fixture.txt","content":"kept capability"}`)); err != nil || strings.Contains(result, "Error") {
		t.Fatalf("allowed primitive lost: %s %v", result, err)
	}
}
