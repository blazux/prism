package tasks

import "testing"

// "low" must round-trip distinctly from "normal" — they used to both write
// as Todoist priority 1, silently collapsing an explicit "low" into "normal"
// with no way to recover it on the next read.
func TestTodoistPriorityRoundTrip(t *testing.T) {
	for _, p := range []string{"high", "normal", "low"} {
		got := fromTodoistPriority(toTodoistPriority(p))
		if got != p {
			t.Errorf("round-trip %q: toTodoistPriority=%d, fromTodoistPriority=%q, want %q",
				p, toTodoistPriority(p), got, p)
		}
	}
	if toTodoistPriority("low") == toTodoistPriority("normal") {
		t.Fatal("low and normal must map to distinct Todoist priority values")
	}
}
