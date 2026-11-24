package agent

import "strings"

// Only the instruction prefix is cached. Every value used to build it is part
// of the key, so persona/profile edits invalidate it without a separate
// mutation protocol. Session identity lives in the dynamic suffix instead of
// changing the shared instruction prefix. Live knowledge, permissions, tools
// and view context remain refreshed on every model call, including changes made
// outside this agent.
type systemPromptPrefixKey struct {
	name, persona, profile string
}

func (a *Agent) cachedSystemPromptPrefix(key systemPromptPrefixKey) string {
	a.promptCacheMu.Lock()
	defer a.promptCacheMu.Unlock()
	if a.promptCache != "" && a.promptCacheKey == key {
		return a.promptCache
	}
	a.promptCache = buildSystemPromptPrefix(key)
	a.promptCacheKey = key
	return a.promptCache
}

func buildSystemPromptPrefix(key systemPromptPrefixKey) string {
	var sb strings.Builder
	if key.name != "" {
		sb.WriteString("Your name is ")
		sb.WriteString(key.name)
		sb.WriteString(".\n\n")
	}
	// Persona changes the voice, never the generalist role or authorization.
	sb.WriteString(systemPromptRole)
	sb.WriteString(systemPromptDecision)
	if strings.TrimSpace(key.persona) != "" {
		sb.WriteString("\n\n")
		sb.WriteString(key.persona)
	}
	lean := key.profile != "guided"
	if key.profile == "minimal" {
		sb.WriteString(systemPromptMinimal)
		sb.WriteString(minimalSafetyPrompt())
	} else {
		sb.WriteString(systemPromptCoreFor(lean))
		if lean {
			sb.WriteString(systemPromptRetryLean)
		} else {
			sb.WriteString(systemPromptRetryGuided)
		}
		sb.WriteString(systemPromptCoreTailFor(lean))
	}
	return sb.String()
}
