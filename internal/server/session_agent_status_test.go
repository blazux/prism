package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"prism/internal/memory"
)

func TestAgentIndicatorsIgnoreDirectToolJobs(t *testing.T) {
	for _, multi := range []bool{false, true} {
		t.Run(fmt.Sprintf("multi=%v", multi), func(t *testing.T) {
			ms := securityStore(t)
			ctx := t.Context()
			u := serviceUser
			id := "finance"
			if multi {
				var err error
				u, err = ms.CreateUser(ctx, "owner@example.test", "unused", "Owner", memory.RoleMember, memory.StatusApproved)
				if err != nil {
					t.Fatal(err)
				}
				id = fmt.Sprintf("u%d-finance", u.ID)
			}
			if err := ms.EnsureSession(ctx, id, "Finance", ownerPtr(u)); err != nil {
				t.Fatal(err)
			}
			s := &Server{memStore: ms, cfg: Config{MultiUser: multi}}
			check := func(wantStatus, wantActivity string, wantJobs int) {
				t.Helper()
				w := httptest.NewRecorder()
				s.handleSessions(w, withUser(httptest.NewRequest("GET", "/api/sessions", nil), u))
				if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal(w.Code, w.Body)
				}
				var list struct {
					Sessions []struct{ ID, RunStatus string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, row := range list.Sessions {
					if row.ID == id {
						found = true
						if row.RunStatus != wantStatus {
							t.Fatalf("incorrect workspace badge: %+v, want %q", row, wantStatus)
						}
					}
				}
				if !found {
					t.Fatal("workspace missing from session list")
				}
				w = httptest.NewRecorder()
				s.handleWorkspaceActivity(w, withUser(httptest.NewRequest("GET", "/api/sessions/"+id+"/activity", nil), u), id)
				var activity workspaceActivity
				if err := json.Unmarshal(w.Body.Bytes(), &activity); err != nil || w.Code != 200 {
					t.Fatal(w.Code, w.Body, err)
				}
				if activity.RunStatus != wantActivity || activity.BackgroundJobs != wantJobs {
					t.Fatalf("incorrect agent activity: %+v", activity)
				}
			}
			_, finishTool, err := s.beginSessionJob(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			defer finishTool()
			check("", "idle", 0)
			_, finishOther, err := s.beginAgentJob(ctx, "another-workspace")
			if err != nil {
				t.Fatal(err)
			}
			defer finishOther()
			check("", "idle", 0)
			_, finishAgent, err := s.beginAgentJob(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			check("running", "running", 1)
			finishAgent()
			check("", "idle", 0)
			s.runsMu.Lock()
			tracked := len(s.sessionJobs[id])
			s.runsMu.Unlock()
			if tracked != 1 {
				t.Fatal("direct tool disappeared from deletion/cancellation tracking")
			}
		})
	}
}
