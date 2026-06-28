package agent

import (
	"context"
	"fmt"
	"strings"
)

// searchHistory full-text searches across every past conversation (all
// workspaces, the assistant, Telegram) so the agent can recall what was said or
// decided earlier even when it's not in the current context window.
func (e *ToolExecutor) searchHistory(ctx context.Context, query string, limit int) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("history search unavailable: no database")
	}
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	hits, err := e.memStore.SearchHistory(ctx, query, limit)
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
