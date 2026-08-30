package memory

import (
	"strings"
	"testing"
)

func TestSharedListQuery(t *testing.T) {
	// No kind filter: only the group-ids arg.
	q, args := sharedListQuery([]int64{1, 2, 3}, "")
	if len(args) != 1 || !strings.Contains(q, "group_id = ANY($1)") {
		t.Fatalf("no-kind query wrong: %s | %v", q, args)
	}
	if strings.Contains(q, "si.kind = $2") {
		t.Error("kind clause present when kind empty")
	}
	// With kind: second arg, $2 indexing.
	q, args = sharedListQuery([]int64{5}, "widget")
	if len(args) != 2 || args[1].(string) != "widget" || !strings.Contains(q, "si.kind = $2") {
		t.Fatalf("kind query wrong: %s | %v", q, args)
	}
	// Newest-first + joins group name.
	if !strings.Contains(q, "ORDER BY si.id DESC") || !strings.Contains(q, "JOIN groups g") {
		t.Errorf("query missing order/join: %s", q)
	}
}
