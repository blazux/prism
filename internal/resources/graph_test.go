package resources

import "testing"

func removed(p Plan, id string) bool {
	for _, n := range p.Remove {
		if n.ID == id {
			return true
		}
	}
	return false
}

func TestDedicatedCleanupPreservesSharedLegacyAndData(t *testing.T) {
	nodes := []Node{
		{ID: "widget:a/one", Kind: "widget", Dependencies: []string{"tool:dedicated", "tool:shared", "cron:old", "file:data/x.json"}, Dedicated: []string{"tool:dedicated", "tool:shared", "cron:old", "file:data/x.json"}},
		{ID: "widget:b/two", Kind: "widget", Dependencies: []string{"tool:shared"}},
		{ID: "tool:dedicated", Kind: "tool", Tracked: true},
		{ID: "tool:shared", Kind: "tool", Tracked: true},
		{ID: "cron:old", Kind: "cron"},
		{ID: "file:data/x.json", Kind: "file", Tracked: true},
	}
	p := PlanRemoval(nodes, []string{"widget:a/one"}, nil)
	if !removed(p, "widget:a/one") || !removed(p, "tool:dedicated") {
		t.Fatal(p)
	}
	for _, id := range []string{"tool:shared", "cron:old", "file:data/x.json"} {
		if removed(p, id) {
			t.Fatalf("unsafe automatic removal: %s", id)
		}
	}
}

func TestRetainedConsumerProtectsTransitiveDependencies(t *testing.T) {
	nodes := []Node{
		{ID: "widget:a/one", Kind: "widget", Dependencies: []string{"cron:refresh", "tool:fetch"}, Dedicated: []string{"cron:refresh", "tool:fetch"}},
		{ID: "gallery:1", Kind: "gallery", Dependencies: []string{"cron:refresh"}, Protected: true},
		{ID: "cron:refresh", Kind: "cron", Tracked: true, Dependencies: []string{"tool:fetch"}},
		{ID: "tool:fetch", Kind: "tool", Tracked: true, Dependencies: []string{"file:data/x.json"}},
		{ID: "file:data/x.json", Kind: "file", Tracked: true},
	}
	p := PlanRemoval(nodes, []string{"widget:a/one"}, []string{"file:data/x.json"})
	if len(p.Remove) != 1 || p.Remove[0].ID != "widget:a/one" {
		t.Fatal(p)
	}
}

func TestCleanupCyclesAndProtectedSelections(t *testing.T) {
	nodes := []Node{
		{ID: "tool:a", Kind: "tool", Dependencies: []string{"tool:b"}},
		{ID: "tool:b", Kind: "tool", Dependencies: []string{"tool:a"}},
		{ID: "tool:shipped", Kind: "tool", Protected: true},
	}
	p := PlanRemoval(nodes, nil, []string{"tool:a", "tool:b", "tool:shipped"})
	if len(p.Remove) != 2 || removed(p, "tool:shipped") {
		t.Fatal(p)
	}
	nodes = append(nodes, Node{ID: "widget:other/w", Kind: "widget", Dependencies: []string{"tool:b"}})
	p = PlanRemoval(nodes, nil, []string{"tool:a", "tool:b"})
	if len(p.Remove) != 0 {
		t.Fatal(p)
	}
}
