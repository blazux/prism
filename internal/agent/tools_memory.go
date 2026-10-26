package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var durableMemoryScopeRe = regexp.MustCompile(`^[ug][1-9][0-9]*$`)

// durableMemoryScope keeps personal memory common to the owner's workspaces,
// and group memory common to that group's channels. Never fall back to a
// deployment-wide library/search when a multi-user caller has no identity.
func (e *ToolExecutor) durableMemoryScope() (string, error) {
	if !e.multiUser {
		return "", nil
	}
	scope := e.personalScope()
	if !durableMemoryScopeRe.MatchString(scope) {
		return "", fmt.Errorf("memory unavailable: no user or group identity for this session")
	}
	return scope, nil
}

// searchHistory recalls earlier discussions across the owner's workspaces and
// channels without exposing another user's personal conversations.
func (e *ToolExecutor) searchHistory(ctx context.Context, query string, limit int) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("history search unavailable: no database")
	}
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	scope, err := e.durableMemoryScope()
	if err != nil {
		return "", err
	}
	hits, err := e.memStore.SearchHistory(ctx, query, limit, scope)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No past messages found for %q.", query), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d past message(s) matching %q:\n\n", len(hits), query)
	for _, h := range hits {
		content := strings.Join(strings.Fields(h.Content), " ")
		if r := []rune(content); len(r) > 300 {
			content = string(r[:300]) + "…"
		}
		fmt.Fprintf(&sb, "- [%s · session %q · %s] %s\n", h.CreatedAt.Format("2006-01-02"), h.SessionID, h.Role, content)
	}
	return sb.String(), nil
}
