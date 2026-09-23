package memory

import "strings"

const KeyAgentPromptProfile = "agent_prompt_profile"

func ValidPromptProfile(v string) bool {
	return v == "guided" || v == "standard" || v == "minimal"
}

// Explicit profile takes precedence; missing/legacy configurations retain their mode.
func ResolvePromptProfile(profile string, lean bool) string {
	profile = strings.TrimSpace(profile)
	if ValidPromptProfile(profile) {
		return profile
	}
	if lean {
		return "standard"
	}
	return "guided"
}
