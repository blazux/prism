package agent

import "testing"

func TestParseTime(t *testing.T) {
	good := []string{"2026-07-01", "2026-07-01 09:30", "2026-07-01T09:30", "2026-07-01T09:30:00Z"}
	for _, s := range good {
		if _, err := parseTime(s); err != nil {
			t.Errorf("parseTime(%q) unexpected error: %v", s, err)
		}
	}
	if _, err := parseTime("not a date"); err == nil {
		t.Error("expected error for invalid time")
	}
	if parseTimePtr("nope") != nil {
		t.Error("parseTimePtr should return nil on bad input")
	}
}

func TestParseIDAndIDArg(t *testing.T) {
	if parseID(" 42 ") != 42 {
		t.Error("parseID should trim and parse")
	}
	if parseID("abc") != 0 {
		t.Error("parseID should be 0 on garbage")
	}
	if got := idArg(map[string]interface{}{"id": float64(7)}); got != "7" {
		t.Errorf("idArg number = %q, want 7", got)
	}
	if got := idArg(map[string]interface{}{"id": "9"}); got != "9" {
		t.Errorf("idArg string = %q, want 9", got)
	}
	if got := idArg(map[string]interface{}{}); got != "" {
		t.Errorf("idArg missing = %q, want empty", got)
	}
}
