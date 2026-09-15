package caldav

import "testing"

// A DELETE on a collection href wipes every object it holds, and these ids come
// from a model that can invent one.
func TestObjectInRefusesAnythingButOneObject(t *testing.T) {
	const col = "/calendars/vincent/agenda/"
	for _, id := range []string{
		"/calendars/vincent/agenda/",        // the collection itself
		"/calendars/vincent/agenda",         // same, without the slash
		"/calendars/vincent/",               // its parent
		"/calendars/vincent/autre/ev.ics",   // another collection
		"/calendars/vincent/agenda/a/b.ics", // deeper than an object can live
		"ev.ics",                            // relative, prefix unverifiable
		"",
		"   ",
	} {
		if got, err := ObjectIn(col, id); err == nil {
			t.Errorf("accepted %q as a single object (got %q)", id, got)
		}
	}

	for _, id := range []string{
		"/calendars/vincent/agenda/ev.ics",
		"/calendars/vincent/agenda/prism-1750000000.ics",
	} {
		got, err := ObjectIn(col, id)
		if err != nil || got != id {
			t.Errorf("%q: got %q err=%v", id, got, err)
		}
	}

	// A collection that was never resolved must not let anything through.
	if _, err := ObjectIn("", "/calendars/vincent/agenda/ev.ics"); err == nil {
		t.Error("an unresolved collection accepted a delete")
	}
	// The collection may be given with or without its trailing slash.
	if _, err := ObjectIn("/calendars/vincent/agenda", "/calendars/vincent/agenda/ev.ics"); err != nil {
		t.Errorf("legitimate object refused: %v", err)
	}
}
