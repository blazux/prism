package memory

import "testing"

// Single-user Prism must keep reading and writing the exact keys it always has:
// an unscoped store, and ConfigScope("global"), have to leave the key untouched.
// If this ever prefixes by accident, every existing config value and secret in the
// database silently becomes invisible — which is precisely how a fork left orphan
// "u1:telegram_bot_token" rows behind that this build could never see.
func TestConfigScope_GlobalLeavesKeysUntouched(t *testing.T) {
	s := &Store{}

	for _, scope := range []string{"", "global"} {
		if got := s.ConfigScope(scope); got != s {
			t.Errorf("ConfigScope(%q) returned a different store — keys would get prefixed", scope)
		}
	}
	if s.cfgScope != "" {
		t.Errorf("an unscoped store must not prefix: cfgScope = %q", s.cfgScope)
	}
	// The seam still has to work for the multi-user build.
	if got := s.ConfigScope("u1"); got.cfgScope != "u1:" {
		t.Errorf("ConfigScope(\"u1\").cfgScope = %q, want \"u1:\"", got.cfgScope)
	}
	// …and must not mutate the store it came from.
	if s.cfgScope != "" {
		t.Errorf("ConfigScope mutated the receiver: cfgScope = %q", s.cfgScope)
	}
}

func TestConfigScope_NilStoreIsSafe(t *testing.T) {
	var s *Store
	if s.ConfigScope("u1") != nil {
		t.Error("ConfigScope on a nil store must stay nil")
	}
}
