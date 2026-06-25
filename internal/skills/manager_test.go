package skills

import "testing"

func TestManagerRoundTrip(t *testing.T) {
	m := NewManager(t.TempDir())

	if list, _ := m.List(); len(list) != 0 {
		t.Fatalf("expected empty, got %d", len(list))
	}

	if err := m.Save(Skill{Name: "Deploy Grafana", WhenToUse: "monitoring dashboards", Body: "1. docker_run grafana"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// name is required
	if err := m.Save(Skill{Name: "", Body: "x"}); err == nil {
		t.Fatal("expected error for empty name")
	}
	// body is required
	if err := m.Save(Skill{Name: "x"}); err == nil {
		t.Fatal("expected error for empty body")
	}

	s, ok, err := m.Get("Deploy Grafana")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if s.WhenToUse != "monitoring dashboards" {
		t.Fatalf("unexpected: %+v", s)
	}
	// name sanitization is stable across get
	if _, ok, _ := m.Get("deploy-grafana"); !ok {
		t.Fatal("expected sanitized name to resolve to same skill")
	}

	if list, _ := m.List(); len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}

	if err := m.Delete("Deploy Grafana"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := m.Get("Deploy Grafana"); ok {
		t.Fatal("expected gone after delete")
	}
	// deleting a missing skill is not an error
	if err := m.Delete("nope"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}
