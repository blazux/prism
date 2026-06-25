package agent

import "testing"

func TestParseStringArray(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`["a","b","c"]`, []string{"a", "b", "c"}},
		{"prose then [\"x\", \"y\"] trailing", []string{"x", "y"}},
		{"- first\n- second\n3. third", []string{"first", "second", "third"}},
		{`["  spaced  ", ""]`, []string{"spaced"}},
	}
	for _, c := range cases {
		got := parseStringArray(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("parseStringArray(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("parseStringArray(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestParseJSONObject(t *testing.T) {
	obj := parseJSONObject("here you go: {\"summary\": \"ok\", \"evidence\": \"e\"} done")
	if obj == nil {
		t.Fatal("expected object, got nil")
	}
	if jstr(obj, "summary") != "ok" || jstr(obj, "evidence") != "e" {
		t.Fatalf("unexpected fields: %+v", obj)
	}
	if parseJSONObject("no json here") != nil {
		t.Fatal("expected nil for non-JSON input")
	}
}

func TestDrLowQuality(t *testing.T) {
	low := []string{"", "n/a", "This page is NOT RELEVANT to the goal", "no information found"}
	for _, s := range low {
		if !drLowQuality(s) {
			t.Errorf("expected low-quality: %q", s)
		}
	}
	if drLowQuality("Paris has a population of about 2.1 million people.") {
		t.Error("expected a substantive summary to pass")
	}
}
