package server

import (
	"encoding/json"
	"net/http/httptest"
	"prism/internal/memory"
	"strings"
	"testing"
)

func TestPromptProfilePersistenceAndMigration(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	request := func(method, body string, status int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleAgentLimits(w, withUser(httptest.NewRequest(method, "/api/agent/limits", strings.NewReader(body)), &memory.User{ID: 0}))
		if w.Code != status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if p := request("GET", "", 200)["promptProfile"]; p != "guided" {
		t.Fatal(p)
	}
	ms.SetConfig(t.Context(), memory.KeyAgentLeanPrompt, "on")
	if p := request("GET", "", 200)["promptProfile"]; p != "standard" {
		t.Fatal(p)
	}
	request("POST", `{"promptProfile":"minimal"}`, 200)
	request("POST", `{"thinking":false}`, 200)
	request("POST", `{"promptProfile":"invalid"}`, 400)
	if p := request("GET", "", 200)["promptProfile"]; p != "minimal" {
		t.Fatal(p)
	}
	request("POST", `{"leanPrompt":false}`, 200)
	if p := request("GET", "", 200)["promptProfile"]; p != "guided" {
		t.Fatal(p)
	}
	group, err := ms.CreateGroup(t.Context(), "profile-fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"", "minimal", "standard", "guided"} {
		cfg := memory.RoomConfig{GroupID: group.ID, AgentLean: true, AgentPromptProfile: profile}
		if err := ms.SetRoomConfig(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		got, err := ms.GetRoomConfig(t.Context(), group.ID)
		if err != nil || got.AgentPromptProfile != profile {
			t.Fatalf("room profile: %+v %v", got, err)
		}
		want := memory.ResolvePromptProfile(profile, true)
		if roomLimits(got).PromptProfile != want {
			t.Fatal("room override lost")
		}
	}
}
