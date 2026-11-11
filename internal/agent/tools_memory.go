package agent

import (
	"context"
	"encoding/json"
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
		fmt.Fprintf(&sb, "- [id=%d · %s · session %q · %s] %s\n", h.ID, h.CreatedAt.Format("2006-01-02"), h.SessionID, h.Role, content)
	}
	return sb.String(), nil
}

func (e *ToolExecutor) readHistory(ctx context.Context, session string, before int64, limit int, messageID int64, offset int) (string, error) {
	if e.memStore == nil {
		return "", fmt.Errorf("history unavailable: no database")
	}
	scope, err := e.durableMemoryScope()
	if err != nil {
		return "", err
	}
	allowed := scope == "" || strings.HasPrefix(scope, "u") && strings.HasPrefix(session, scope+"-")
	if strings.HasPrefix(scope, "g") {
		allowed = session == "room-"+scope || strings.HasPrefix(session, "webex-"+scope+"-") || strings.HasPrefix(session, "teams-"+scope+"-")
	}
	if !allowed {
		return "", fmt.Errorf("conversation access denied")
	}
	if messageID < 0 || offset < 0 {
		return "", fmt.Errorf("message_id and offset must be non-negative")
	}
	if offset > 0 && messageID == 0 {
		return "", fmt.Errorf("offset requires message_id from a search result")
	}
	if messageID > 0 {
		before = messageID + 1
		limit = 1
	}
	if limit < 1 || limit > 50 {
		limit = 15
	}
	page, err := e.memStore.LoadHistoryPage(ctx, session, before, limit)
	if err != nil {
		return "", err
	}
	if messageID > 0 {
		if len(page.Entries) != 1 || page.Entries[0].ID != messageID {
			return "", fmt.Errorf("message no longer available in this conversation")
		}
		original, err := json.Marshal(page.Entries[0])
		if err != nil {
			return "", err
		}
		characters := []rune(string(original))
		if offset > len(characters) {
			return "", fmt.Errorf("offset is past the end of this message")
		}
		end := min(offset+16000, len(characters))
		for {
			var next any
			if end < len(characters) {
				next = end
			}
			result, err := json.Marshal(map[string]any{"session": session, "message_id": messageID, "offset": offset, "text": string(characters[offset:end]), "nextOffset": next})
			if err != nil || len(result) <= 18000 || end == offset {
				return string(result), err
			}
			end = offset + (end-offset)/2
		}
	}
	b, err := json.Marshal(map[string]any{"session": session, "messages": page.Entries, "before": page.Before, "hasMore": page.HasMore})
	return string(b), err
}
