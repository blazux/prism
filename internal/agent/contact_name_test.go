package agent

import "testing"

// "Appelle le plombier" is what a user actually says, and the name they say is
// rarely spelled the way it was typed into the directory.
func TestNormalizeContactName(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"Le Plombier", "le plombier"},
		{"  le   plombier  ", "le plombier"},
		{"Jean-Marc", "jean marc"},
		{"L'Atelier", "l atelier"},
	} {
		if normalizeContactName(tc.a) != normalizeContactName(tc.b) {
			t.Errorf("%q and %q should match, got %q vs %q",
				tc.a, tc.b, normalizeContactName(tc.a), normalizeContactName(tc.b))
		}
	}
	if normalizeContactName("le plombier") == normalizeContactName("le peintre") {
		t.Error("different contacts collapsed to the same name")
	}
}
