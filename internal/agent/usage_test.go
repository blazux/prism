package agent

import (
	"context"
	"testing"
)

func TestTaskIDInheritedByChild(t *testing.T) {
	parent := withTaskID(context.Background(), "parent-task")
	child := withTaskID(parent, "child-message")
	if got := taskIDFromContext(child); got != "parent-task" {
		t.Fatalf("child got task ID %q", got)
	}
}
