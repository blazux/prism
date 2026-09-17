package server

import (
	"context"
	"prism/internal/agent"
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

// An internal call must never fall back to the switchboard persona: being told
// "you have reached the switchboard" by an agent that just greeted you by name is
// worse than a generic assistant. With no store, no group and no configured text,
// the built-in internal persona is what must come out.
func TestInternalPersonaNeverFallsBackToSwitchboard(t *testing.T) {
	s := &Server{}
	got := s.voiceInternalPersonality(context.Background(), &memory.User{ID: 1, DisplayName: "Vincent"})
	if got != defaultInternalVoicePersonality {
		t.Fatalf("internal persona = %q, want the built-in internal text", got)
	}
	if strings.Contains(got, "standardiste") {
		t.Fatal("a recognised caller was handed the switchboard persona")
	}
	if s.voiceInternalPersonality(context.Background(), nil) != defaultInternalVoicePersonality {
		t.Fatal("an unidentified caller must still get a usable persona, never an empty prompt")
	}
}

// The switchboard reads ONE collection, the one an admin pointed at. Storing the
// fully-scoped name matters: it is what gets searched, and "horaires" in the
// reserved scope is not the same corpus as "horaires" in a group.
func TestVoiceKBCollectionIsStoredScoped(t *testing.T) {
	for _, tc := range []struct{ scope, name, want string }{
		{voiceGuestScope, "horaires", "voice--horaires"},
		{"g3", "procédures", "g3--procédures"},
	} {
		got := agent.ScopeCollection(tc.scope, tc.name)
		if got != tc.want {
			t.Fatalf("ScopeCollection(%q, %q) = %q, want %q", tc.scope, tc.name, got, tc.want)
		}
		if back := agent.UnscopeCollection(tc.scope, got); back != tc.name {
			t.Fatalf("round trip lost the name: %q", back)
		}
	}
	// Two collections that display the same but are not the same corpus.
	if agent.ScopeCollection(voiceGuestScope, "faq") == agent.ScopeCollection("g3", "faq") {
		t.Fatal("a group collection and a switchboard collection collapsed to one name")
	}
}
