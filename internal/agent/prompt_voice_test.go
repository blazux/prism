package agent

import (
	"context"
	"strings"
	"testing"
)

func TestVoicePromptKeepsContextWithoutDashboardInstructions(t *testing.T) {
	for _, profile := range []string{"guided", "standard", "minimal"} {
		t.Run(profile, func(t *testing.T) {
			a := &Agent{
				executor: &ToolExecutor{}, channel: voiceChannel, agentName: "Fixture",
				personality: "Directory: Alice, extension 1001.", basePersonality: "Use a friendly tone.",
				limits:        Limits{PromptProfile: profile},
				userProfileFn: func() string { return "CALLER_PROFILE" },
				ragCtxFn:      func() string { return "SCOPED_KNOWLEDGE" },
			}
			unavailable := func() string { t.Error("voice loaded dashboard context"); return "" }
			a.skillsCtxFn, a.servicesCtxFn, a.mcpCtxFn, a.globalCtxFn, a.viewCtxFn = unavailable, unavailable, unavailable, unavailable, unavailable
			prompt, prefix := a.buildSystemPromptWithCachePrefix(context.Background(), "RELEVANT_LESSON")
			for _, want := range []string{"Fixture", "Use a friendly tone.", "Directory: Alice", "CALLER_PROFILE", "SCOPED_KNOWLEDGE", "RELEVANT_LESSON", "transfer_call", "take_message", "end_call", "rag_search", "WAIT", "confirmed outcome", "Do not reveal secrets"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("missing voice context/contract %q", want)
				}
			}
			if strings.Contains(prompt, "prismTool(") || strings.Contains(prompt, "exec_command") || len(prompt) > 5000 {
				t.Fatalf("dashboard instructions leaked into voice prompt (%d bytes)", len(prompt))
			}
			if prefix == 0 || prefix >= len(prompt) || strings.Contains(prompt[:prefix], "CALLER_PROFILE") {
				t.Fatal("live caller context entered the stable cache prefix")
			}
			a.channel = ""
			// Compare only prompt instructions, without dashboard callback work.
			a.skillsCtxFn, a.servicesCtxFn, a.mcpCtxFn, a.globalCtxFn, a.viewCtxFn = nil, nil, nil, nil, nil
			dashboard := a.buildSystemPrompt(context.Background(), "RELEVANT_LESSON")
			if !strings.Contains(dashboard, "prismTool(") {
				t.Fatal("dashboard lost its original instructions")
			}
			t.Logf("%s: voice=%d bytes dashboard=%d bytes", profile, len(prompt), len(dashboard))
		})
	}
}
