package server

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"+1 868 555 0123":  "18685550123",
		"(868) 555-0123":   "8685550123",
		"06 12 34 56 78":   "0612345678",
		"tel:+33612345678": "33612345678",
		"":                 "",
	}
	for in, want := range cases {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

// The voice channel must never expose a dangerous tool, whoever is calling —
// guest and identified allow-lists both stay clear of host/code/secret access.
func TestVoiceAllowlistsAreSafe(t *testing.T) {
	dangerous := []string{"exec_command", "docker_run", "docker_manage", "delete_file",
		"write_file", "secrets", "email", "install_packages", "register_tool"}
	for _, set := range []map[string]bool{voiceGuestAllowedTools, voiceKnownAllowedTools} {
		for _, d := range dangerous {
			if set[d] {
				t.Errorf("dangerous tool %q must not be allowed on the voice channel", d)
			}
		}
	}
	// The guest is strictly a subset of the identified caller.
	for tool := range voiceGuestAllowedTools {
		if !voiceKnownAllowedTools[tool] {
			t.Errorf("guest tool %q should also be allowed for an identified caller", tool)
		}
	}
}

// A known caller must be hidden every built-in outside their allow-list.
func TestVoiceHiddenToolsHidesDangerous(t *testing.T) {
	hidden := voiceHiddenTools(voiceKnownAllowedTools)
	if !hidden["exec_command"] {
		t.Error("exec_command must be hidden on a voice call")
	}
	if hidden["rag_search"] {
		t.Error("rag_search must NOT be hidden (it is allow-listed)")
	}
}
