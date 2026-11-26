package agent

import (
	"errors"
	"strings"
	"testing"

	"prism/internal/email"
)

func TestEmailViewAccountGuardRunsBeforeMailboxOperations(t *testing.T) {
	store := resourceDatabase(t)
	executor := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	executor.SetMemoryStore(store)
	executor.SetPersonalScope("u7")
	if err := executor.userStore().SetConfig(t.Context(), emailConfigKey, `{"imap_host":"never-contact.invalid","smtp_host":"never-contact.invalid","user":"current@example.test"}`); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"list", "read", "search", "send", "reply", "folders", "create_folder", "move", "archive", "trash", "mark_read", "mark_unread"} {
		_, err := executor.emailTool(t.Context(), map[string]interface{}{"action": action, "account": "a-previous-mailbox", "uid": float64(1), "query": "fixture"})
		if !errors.Is(err, email.ErrAccountChanged) {
			t.Fatalf("%s did not reject the old mailbox before contacting IMAP/SMTP: %v", action, err)
		}
	}
	for _, expected := range []string{"", (email.Config{IMAPHost: "never-contact.invalid", SMTPHost: "never-contact.invalid", User: "current@example.test"}).AccountFingerprint()} {
		_, err := executor.emailTool(t.Context(), map[string]interface{}{"action": "move", "account": expected})
		if err == nil || !strings.Contains(err.Error(), "uid") || errors.Is(err, email.ErrAccountChanged) {
			t.Fatal("compatible callers did not reach ordinary UID validation", err)
		}
	}
}
