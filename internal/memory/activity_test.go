package memory

import (
	"strings"
	"testing"
)

func TestBuildActivityQuery(t *testing.T) {
	// Defaults: all human-relevant kinds, limit clamped, kinds is $1, limit last.
	q, args := buildActivityQuery(ActivityFilter{})
	if len(args) != 2 {
		t.Fatalf("default: want 2 args (kinds,limit), got %d: %v", len(args), args)
	}
	if !strings.Contains(q, "kind = ANY($1)") || !strings.Contains(q, "LIMIT $2") {
		t.Errorf("default query wrong: %s", q)
	}
	if args[1].(int) != 50 {
		t.Errorf("limit not defaulted to 50: %v", args[1])
	}
	if got := args[0].([]string); len(got) != 4 {
		t.Errorf("default kinds should be the 4 human-relevant ones, got %v", got)
	}

	// Every filter set: $N indexing must stay consistent and LIMIT must be last.
	q, args = buildActivityQuery(ActivityFilter{
		Kinds:   []string{"audit"},
		Items:   []string{"tool_error", "tool_denied"},
		UserID:  42,
		Session: "u42-main",
		Before:  1000,
		Limit:   20,
	})
	// kinds $1, items $2, user $3, session $4, before $5, limit $6
	for i, frag := range []string{"kind = ANY($1)", "item = ANY($2)", "user_id = $3", "session = $4", "id < $5", "LIMIT $6"} {
		if !strings.Contains(q, frag) {
			t.Errorf("fragment %d missing (%q) in: %s", i, frag, q)
		}
	}
	if len(args) != 6 {
		t.Fatalf("want 6 args, got %d: %v", len(args), args)
	}
	if args[5].(int) != 20 {
		t.Errorf("limit arg wrong: %v", args[5])
	}
	if args[2].(int64) != 42 || args[4].(int64) != 1000 {
		t.Errorf("user/before args wrong: %v", args)
	}

	// Over-large limit is clamped.
	_, args = buildActivityQuery(ActivityFilter{Limit: 9999})
	if args[len(args)-1].(int) != 50 {
		t.Errorf("limit 9999 should clamp to 50, got %v", args[len(args)-1])
	}
}
