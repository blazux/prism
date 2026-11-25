package agent

import (
	"strings"
	"testing"
)

func TestWidgetDataContractsAcrossPromptProfiles(t *testing.T) {
	for _, profile := range []string{"guided", "standard", "minimal"} {
		t.Run(profile, func(t *testing.T) {
			a := &Agent{executor: &ToolExecutor{}, limits: Limits{PromptProfile: profile}}
			prompt := a.buildSystemPrompt(t.Context(), "")
			for _, contract := range []string{
				"persist application data on the server",
				"never the only copy of user content",
				"workspace files are shared storage, not per-user isolation",
				"Existing browser-only data needs an actual export/import",
			} {
				if !strings.Contains(prompt, contract) {
					t.Errorf("missing widget data contract: %s", contract)
				}
			}
			if strings.Contains(prompt, "scoped per board") {
				t.Error("personal app data must not be described as a separate store per board")
			}
		})
	}
}

func TestWidgetStorageRecipeAvailableInRuntimeHelp(t *testing.T) {
	for _, contract := range []string{
		"File backends need locking and atomic writes",
		"not a private user store",
		"prism_help topic=data-events",
		"scoped to the signed-in user across boards",
	} {
		if !strings.Contains(RuntimeHelp(), contract) {
			t.Errorf("runtime help lost persistence guidance: %s", contract)
		}
	}
}
