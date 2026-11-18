package server

import (
	"strings"
	"testing"

	"prism/internal/memory"
)

func TestEmailHelpStatusDistinguishesSavedConfigurationFromConnection(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	ctx := t.Context()
	// A saved configuration can refer to an unreachable server and have no
	// password. It must not be advertised as a working mailbox connection.
	config := `{"imap_host":"unreachable.invalid","smtp_host":"unreachable.invalid","user":"fixture@example.test"}`
	if err := ms.SetConfig(ctx, "email_config", config); err != nil {
		t.Fatal(err)
	}
	check := func(user *memory.User, configured bool) {
		t.Helper()
		status := s.integrationsStatusFor(user)(ctx)
		var email string
		for _, line := range strings.Split(status, "\n") {
			if strings.HasPrefix(line, "- Email: ") {
				email = line
				break
			}
		}
		if configured {
			if !strings.HasPrefix(email, "- Email: configured;") || !strings.Contains(email, "connection not tested") || !strings.Contains(email, "email action=list") {
				t.Fatalf("saved settings do not establish a working connection: %q", email)
			}
			if strings.Contains(email, "connected") || strings.Contains(email, "tool works") {
				t.Fatalf("help claimed unverified connectivity: %q", email)
			}
		} else if !strings.HasPrefix(email, "- Email: not configured") {
			t.Fatalf("another owner's mailbox settings leaked into help: %q", email)
		}
	}
	check(nil, true)
	check(&memory.User{ID: 0}, true)
	user := &memory.User{ID: 7}
	check(user, false)
	if err := ms.ConfigScope("u7").SetConfig(ctx, "email_config", config); err != nil {
		t.Fatal(err)
	}
	check(user, true)
}
