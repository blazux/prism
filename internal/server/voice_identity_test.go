package server

import (
	"errors"
	"testing"

	"prism/internal/memory"
)

func TestVoicePhoneFormatsAndAmbiguousProfiles(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	create := func(email, phone, status string) *memory.User {
		t.Helper()
		u, err := ms.CreateUser(ctx, email, "fixture", email, memory.RoleMember, status)
		if err != nil {
			t.Fatal(err)
		}
		if err := ms.UpdateProfile(ctx, u.ID, email, "", "", phone, nil); err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner := create("owner@example.test", "+596 596 12 34 56", memory.StatusApproved)
	s := &Server{memStore: ms, cfg: Config{MultiUser: true}}
	for _, phone := range []string{"0596123456", "+596596123456", "00596596123456", "596123456"} {
		if u := s.resolveVoiceCaller(ctx, phone); u == nil || u.ID != owner.ID {
			t.Fatalf("equivalent format %q did not identify the owner", phone)
		}
	}
	create("pending@example.test", "0596123456", memory.StatusPending)
	create("disabled@example.test", "0596123456", memory.StatusDisabled)
	if u := s.resolveVoiceCaller(ctx, "0596123456"); u == nil || u.ID != owner.ID {
		t.Fatal("inactive accounts affected identity")
	}
	other := create("other@example.test", "0596123456", memory.StatusApproved)
	for _, phone := range []string{"0596123456", "+596596123456", "123456"} {
		if u := s.resolveVoiceCaller(ctx, phone); u != nil {
			t.Fatalf("ambiguous number %q selected user %d", phone, u.ID)
		}
	}
	if _, err := ms.UserByPhone(ctx, "596123456"); !errors.Is(err, memory.ErrUserNotFound) {
		t.Fatalf("ambiguity not reported as unknown: %v", err)
	}
	if err := ms.SetUserStatus(ctx, other.ID, memory.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if u := s.resolveVoiceCaller(ctx, "0596123456"); u == nil || u.ID != owner.ID {
		t.Fatal("unique owner not restored")
	}
	for _, phone := range []string{"", "12345", "0596999999"} {
		if u := s.resolveVoiceCaller(ctx, phone); u != nil {
			t.Fatalf("unknown number %q selected user", phone)
		}
	}
}
