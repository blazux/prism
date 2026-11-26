package email

import (
	"encoding/json"
	"testing"
)

func TestEmailCategoriesStayWithMailboxAndPreserveLegacyCopies(t *testing.T) {
	ctx := t.Context()
	store := ruleMemory{"email_tags": `{"1":{"category":"Factures"}}`, "email_tags:Projects": `{"1":{"tags":["été"]}}`}
	if err := BindLegacyTags(ctx, store, "mailbox-A"); err != nil {
		t.Fatal(err)
	}
	// Switching to B before the first read must not attach A's old UID tags to B.
	b, err := LoadTags(ctx, store, "mailbox-B", "INBOX")
	if err != nil || len(b) != 0 {
		t.Fatal("legacy tags attached to the replacement mailbox", b, err)
	}
	if err := MergeTags(ctx, store, "mailbox-B", "INBOX", map[string]json.RawMessage{"1": json.RawMessage(`{"category":"Personal"}`)}); err != nil {
		t.Fatal(err)
	}
	a, err := LoadTags(ctx, store, "mailbox-A", "INBOX")
	if err != nil || string(a["1"]) != `{"category":"Factures"}` {
		t.Fatal("original account categories lost", a, err)
	}
	projects, err := LoadTags(ctx, store, "mailbox-A", "Projects")
	if err != nil || string(projects["1"]) != `{"tags":["été"]}` {
		t.Fatal("legacy folder categories lost", projects, err)
	}
	b, err = LoadTags(ctx, store, "mailbox-B", "INBOX")
	if err != nil || string(b["1"]) != `{"category":"Personal"}` {
		t.Fatal("second mailbox tags lost", b, err)
	}
	if store["email_tags"] != `{"1":{"category":"Factures"}}` || store["email_tags:Projects"] != `{"1":{"tags":["été"]}}` {
		t.Fatal("legacy backup keys were changed")
	}
	if err := MergeTags(ctx, store, "mailbox-A", "INBOX", map[string]json.RawMessage{"1": json.RawMessage(`null`)}); err != nil {
		t.Fatal(err)
	}
	a, err = LoadTags(ctx, store, "mailbox-A", "INBOX")
	if err != nil || len(a) != 0 {
		t.Fatal("deleted tag reappeared from the legacy backup", a, err)
	}
	otherUser := ruleMemory{"email_tags": `{"1":{"category":"Other user"}}`}
	other, err := LoadTags(ctx, otherUser, "mailbox-A", "INBOX")
	if err != nil || string(other["1"]) != `{"category":"Other user"}` {
		t.Fatal("account namespace escaped the user store", other, err)
	}
}
