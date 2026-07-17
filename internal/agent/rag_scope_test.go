package agent

import "testing"

func TestRAGScopeColUncol(t *testing.T) {
	e := NewToolExecutor(nil, "", "", "", "")

	// No scope (legacy/single-user): names pass through unchanged.
	if got := e.col("docs"); got != "docs" {
		t.Errorf(`col with empty scope = %q, want "docs"`, got)
	}

	e.SetRAGScope("g5")
	if got := e.col("docs"); got != "g5--docs" {
		t.Errorf(`col = %q, want "g5--docs"`, got)
	}
	if got := e.uncol("g5--docs"); got != "docs" {
		t.Errorf(`uncol = %q, want "docs"`, got)
	}
	// Round-trip.
	if got := e.uncol(e.col("my-kb")); got != "my-kb" {
		t.Errorf(`round-trip = %q, want "my-kb"`, got)
	}
	// Two different scopes never collide for the same display name.
	a := NewToolExecutor(nil, "", "", "", "")
	a.SetRAGScope("g5")
	b := NewToolExecutor(nil, "", "", "", "")
	b.SetRAGScope("g7")
	if a.col("docs") == b.col("docs") {
		t.Error("collections in different group scopes must not share a storage key")
	}
}
