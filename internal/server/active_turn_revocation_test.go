package server

import (
	"context"
	"fmt"
	"prism/internal/memory"
	"testing"
)

func TestActiveTurnChecksRevocationAfterOrdinaryDisconnect(t *testing.T) {
	for _, change := range []string{"disable", "demote"} {
		t.Run(change, func(t *testing.T) {
			ms := securityStore(t)
			u, err := ms.CreateUser(t.Context(), change+"@example.test", "unused", "Fixture", memory.RoleGlobalAdmin, memory.StatusApproved)
			if err != nil {
				t.Fatal(err)
			}
			s := &Server{memStore: ms, cfg: Config{MultiUser: true}}
			ctx, cancel := context.WithCancel(t.Context())
			cc := s.callerContextForUser(ctx, u, fmt.Sprintf("u%d-default", u.ID))
			cancel() // Losing the browser is allowed; the turn belongs to the server.
			if err := cc.Guard("read_file", map[string]interface{}{"path": "fixture"}); err != nil {
				t.Fatalf("disconnect revoked a valid turn: %v", err)
			}
			if change == "disable" {
				err = ms.SetUserStatus(t.Context(), u.ID, memory.StatusDisabled)
			} else {
				err = ms.SetUserRole(t.Context(), u.ID, memory.RoleMember)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := cc.Guard("read_file", map[string]interface{}{"path": "fixture"}); err == nil {
				t.Fatal("revoked account kept active-turn tool access")
			}
		})
	}
}

func TestActiveTurnChecksGroupPermissionChanges(t *testing.T) {
	for _, change := range []string{"remove", "demote"} {
		t.Run(change, func(t *testing.T) {
			ms := securityStore(t)
			u, err := ms.CreateUser(t.Context(), change+"@example.test", "unused", "Fixture", memory.RoleMember, memory.StatusApproved)
			if err != nil {
				t.Fatal(err)
			}
			group, err := ms.CreateGroup(t.Context(), "Fixture group")
			if err != nil {
				t.Fatal(err)
			}
			if err := ms.AddGroupMember(t.Context(), group.ID, u.ID, memory.GroupRoleAdmin); err != nil {
				t.Fatal(err)
			}
			s := &Server{memStore: ms, cfg: Config{MultiUser: true}}
			cc := s.callerContextForUser(t.Context(), u, fmt.Sprintf("u%d-default", u.ID))
			if err := cc.Guard("exec_command", map[string]interface{}{}); err != nil {
				t.Fatal(err)
			}
			if change == "remove" {
				err = ms.RemoveGroupMember(t.Context(), group.ID, u.ID)
			} else {
				err = ms.AddGroupMember(t.Context(), group.ID, u.ID, memory.GroupRoleMember)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := cc.Guard("exec_command", map[string]interface{}{}); err == nil {
				t.Fatal("stale group-admin grant remained usable")
			}
		})
	}
}
