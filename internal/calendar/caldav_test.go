package calendar

import (
	"strings"
	"testing"
)

// Update must recover the same UID Add embedded in the href it returned
// (ObjectPath = uid + ".ics"), so a PUT to that href is a true in-place
// overwrite, not a new object under a new identity — this is the one part
// of Update's logic that isn't a straight mirror of an already-proven
// Add/Delete pattern, so it's the one worth a dedicated test.
func TestUIDFromObjectPath(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want string // "" means "expect some fresh UID was generated instead"
	}{
		{"prism-created event", "/calendars/user/events/prism-1737558123456.ics", "prism-1737558123456"},
		{"nested collection path", "/dav/calendars/personal/work/prism-42.ics", "prism-42"},
		{"foreign event, non-Prism naming", "/calendars/user/events/some-other-client-abcdef.ics", "some-other-client-abcdef"},
		{"empty id", "", ""},
	}
	for _, c := range cases {
		got := uidFromObjectPath(c.id)
		if got == "" {
			t.Errorf("%s: uidFromObjectPath(%q) returned empty — must always return a usable UID", c.name, c.id)
			continue
		}
		if c.want != "" && got != c.want {
			t.Errorf("%s: uidFromObjectPath(%q) = %q, want %q", c.name, c.id, got, c.want)
		}
		if strings.Contains(got, "/") {
			t.Errorf("%s: uid %q must not contain a path separator", c.name, got)
		}
	}
}
