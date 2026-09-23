package timeprefs

import (
	"context"
	"testing"
	"time"
)

type zoneStore string

func (s zoneStore) GetConfig(context.Context, string) (string, bool, error) {
	return string(s), true, nil
}
func TestPersonalTimezones(t *testing.T) {
	for _, zone := range []string{"America/Martinique", "Europe/Paris", "UTC", ""} {
		if err := Validate(zone); err != nil {
			t.Fatal(err)
		}
	}
	for _, zone := range []string{"Local", "Mars/Olympus", "Europe/Paris'", "../UTC"} {
		if Validate(zone) == nil {
			t.Fatalf("accepted %q", zone)
		}
	}
	for _, tc := range []struct{ zone, date, want string }{
		{"America/Martinique", "2026-07-01T09:00", "2026-07-01T13:00:00Z"},
		{"Europe/Paris", "2026-07-01T09:00", "2026-07-01T07:00:00Z"},
		{"Europe/Paris", "2026-01-01T09:00", "2026-01-01T08:00:00Z"},
	} {
		t.Run(tc.zone+tc.date, func(t *testing.T) {
			t.Parallel()
			_, loc := Read(t.Context(), zoneStore(tc.zone))
			got, err := Parse("2006-01-02T15:04", tc.date, loc)
			if err != nil || got.UTC().Format(time.RFC3339) != tc.want {
				t.Fatalf("%v %v", got, err)
			}
			if Location(WithLocation(t.Context(), loc)) != loc {
				t.Fatal("context lost timezone")
			}
		})
	}
	loc, _ := time.LoadLocation("Europe/Paris")
	if _, err := Parse("2006-01-02T15:04", "2026-03-29T02:30", loc); err == nil {
		t.Fatal("accepted nonexistent DST time")
	}
	got, err := Parse(time.RFC3339, "2026-07-01T09:00:00-04:00", loc)
	if err != nil || got.UTC().Hour() != 13 {
		t.Fatal("explicit offset lost")
	}
}
