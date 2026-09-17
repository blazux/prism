package server

import (
	"prism/internal/memory"
	"strings"
	"testing"
)

func TestVoiceDirectoryRequiresUniqueMatch(t *testing.T) {
	entries := []memory.DirEntry{{Name: "Jean Dupont", Phone: "101"}, {Name: "Jean Martin", Phone: "102"}, {Name: "Gérard Blanc", Phone: "103"}, {Name: "Jean-Marc Petit", Phone: "104"}}
	for _, query := range []string{"Jean", "an", "", "inconnu"} {
		if _, ok := resolveTransferName(entries, query); ok {
			t.Fatalf("unsafe resolution of %q", query)
		}
	}
	for query, want := range map[string]string{"gerard": "103", "Marc": "104", "jean marc": "104", "JEAN DUPONT": "101"} {
		if e, ok := resolveTransferName(entries, query); !ok || e.Phone != want {
			t.Fatalf("%q: got %q ok=%v", query, e.Phone, ok)
		}
	}
	// A resolved destination must carry how that person wants to be reached: the
	// relay forwards it to Vox, which owns the announcement machinery.
	withTypes := []memory.DirEntry{
		{Name: "Gérard Blanc", Phone: "103", Transfer: memory.TransferAttended},
		{Name: "Jean Dupont", Phone: "101", Transfer: memory.TransferBlind},
	}
	for query, want := range map[string]string{"gerard": memory.TransferAttended, "dupont": memory.TransferBlind} {
		if e, ok := resolveTransferName(withTypes, query); !ok || e.Transfer != want {
			t.Fatalf("%q: transfer = %q, want %q", query, e.Transfer, want)
		}
	}

	entries = append(entries, memory.DirEntry{Name: "Jean Martin", Phone: "105"})
	if _, ok := resolveTransferName(entries, "Jean Martin"); ok {
		t.Fatal("duplicate name was accepted")
	}
}

func TestGatewayContextOnlyOnVoice(t *testing.T) {
	facts := []string{"Le transfert a échoué."}
	if got := voiceTurnContent("Bonjour", facts, false); got != "Bonjour" {
		t.Fatal("browser supplied gateway facts")
	}
	if got := voiceTurnContent("Bonjour", facts, true); !strings.Contains(got, facts[0]) || !strings.Contains(got, "Appelant : Bonjour") {
		t.Fatal("voice facts lost or confused with caller speech")
	}
}
