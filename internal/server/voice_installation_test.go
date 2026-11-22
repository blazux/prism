package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"prism/internal/agent"
	"prism/internal/memory"
	"prism/internal/rag"
)

func TestVoiceInstallationRoutesRequireOwnerOrGlobalAdmin(t *testing.T) {
	routes := []struct {
		method, path, body string
		handler            func(*Server, http.ResponseWriter, *http.Request)
		allowedStatus      int
	}{
		{"GET", "/api/voice", "", (*Server).handleVoiceConfig, http.StatusServiceUnavailable},
		{"PUT", "/api/voice", `{"personality":"changed"}`, (*Server).handleVoiceConfig, http.StatusServiceUnavailable},
		{"GET", "/api/voice/kb", "", (*Server).handleVoiceKB, http.StatusServiceUnavailable},
		{"PUT", "/api/voice/kb", `{"collection":"voice--faq"}`, (*Server).handleVoiceKB, http.StatusServiceUnavailable},
		{"POST", "/api/voice/search", `{"query":"opening hours"}`, (*Server).handleVoiceSearch, http.StatusServiceUnavailable},
		{"GET", "/api/voice/caller?number=0596123456", "", (*Server).handleVoiceCaller, http.StatusOK},
		{"GET", "/api/voice/directory", "", (*Server).handleVoiceDirectory, http.StatusOK},
	}
	for _, tc := range []struct {
		name  string
		multi bool
		user  *memory.User
		allow bool
	}{
		{"anonymous", true, nil, false},
		{"member", true, &memory.User{ID: 2, Role: memory.RoleMember}, false},
		{"global admin", true, &memory.User{ID: 1, Role: memory.RoleGlobalAdmin}, true},
		{"gateway", true, serviceUser, true},
		{"personal owner", false, serviceUser, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: Config{MultiUser: tc.multi}}
			for _, route := range routes {
				r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				if tc.user != nil {
					r = withUser(r, tc.user)
				}
				w := httptest.NewRecorder()
				route.handler(s, w, r)
				want := http.StatusForbidden
				if tc.allow {
					want = route.allowedStatus
				}
				if w.Code != want {
					t.Errorf("%s %s: got %d, want %d", route.method, route.path, w.Code, want)
				}
			}
		})
	}
}

func TestGroupAdminCannotChangeGlobalVoiceConfiguration(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u, err := ms.CreateUser(ctx, "voice-group-admin@example.test", "fixture", "Group admin", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	group, err := ms.CreateGroup(ctx, "Voice fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, u.ID, memory.GroupRoleAdmin); err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, cfg: Config{MultiUser: true}}
	if !s.isAdminUser(ctx, u) {
		t.Fatal("fixture must exercise a real group administrator")
	}
	for _, route := range []struct {
		method, path, body string
		handler            func(http.ResponseWriter, *http.Request)
	}{
		{"GET", "/api/voice", "", s.handleVoiceConfig},
		{"PUT", "/api/voice", `{"personality":"changed"}`, s.handleVoiceConfig},
		{"GET", "/api/voice/kb", "", s.handleVoiceKB},
		{"PUT", "/api/voice/kb", `{"collection":"voice--faq"}`, s.handleVoiceKB},
		{"POST", "/api/voice/search", `{"query":"opening hours"}`, s.handleVoiceSearch},
		{"GET", "/api/voice/caller?number=0596123456", "", s.handleVoiceCaller},
		{"GET", "/api/voice/directory", "", s.handleVoiceDirectory},
	} {
		r := withUser(httptest.NewRequest(route.method, route.path, strings.NewReader(route.body)), u)
		w := httptest.NewRecorder()
		route.handler(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("group admin reached %s %s: %d", route.method, route.path, w.Code)
		}
	}
}

func TestVoiceKnowledgeSelectionAndSearchUseOnlyPublicInstallationScopes(t *testing.T) {
	ms, dsn := securityStoreWithDSN(t)
	ctx := t.Context()
	store, err := rag.NewStore(ctx, dsn, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	group, err := ms.CreateGroup(ctx, "Knowledge fixture")
	if err != nil {
		t.Fatal(err)
	}
	groupScope := fmt.Sprintf("g%d", group.ID)
	for _, tc := range []struct{ scope, name string }{
		{voiceGuestScope, "hours"}, {groupScope, "services"}, {"u42", "private"}, {"g999999", "orphan"}, {"", "legacy"},
	} {
		if err := store.EnsureCollection(ctx, agent.ScopeCollection(tc.scope, tc.name), tc.scope); err != nil {
			t.Fatal(err)
		}
	}
	// A collection's metadata and stored name must agree about its scope.
	if err := store.EnsureCollection(ctx, "u43--mismatched", voiceGuestScope); err != nil {
		t.Fatal(err)
	}
	public := agent.ScopeCollection(voiceGuestScope, "hours")
	groupCollection := agent.ScopeCollection(groupScope, "services")
	if err := store.UpsertDocument(ctx, public, "hours.txt", "fixture", 12, []string{"Public opening hours"}, []int{0}, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	var embeddings atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embeddings.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
	}))
	t.Cleanup(upstream.Close)
	s := &Server{memStore: ms, ragStore: store, ragEmbedder: rag.NewOpenAIEmbedder(upstream.URL, "", "fixture"), cfg: Config{MultiUser: true}}
	// This global admin is not a member of the group; the installation catalogue
	// must be the same for that admin and Vox's service identity.
	admin := &memory.User{ID: 999, Role: memory.RoleGlobalAdmin}
	request := func(user *memory.User, method, path, body string, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user))
		return w
	}
	for _, user := range []*memory.User{admin, serviceUser} {
		w := request(user, "GET", "/api/voice/kb", "", s.handleVoiceKB)
		if w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
		var out struct{ Choices []voiceKnowledgeChoice }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, choice := range out.Choices {
			found[choice.Value] = true
		}
		if len(found) != 2 || !found[public] || !found[groupCollection] {
			t.Fatalf("unexpected public knowledge catalogue: %v", found)
		}
	}
	for _, collection := range []string{public, groupCollection, ""} {
		w := request(admin, "PUT", "/api/voice/kb", fmt.Sprintf(`{"collection":%q}`, collection), s.handleVoiceKB)
		if w.Code != http.StatusOK {
			t.Fatalf("valid selection %q: %d %s", collection, w.Code, w.Body.String())
		}
		got, _, _ := ms.GetConfig(ctx, cfgVoiceRAGCollection)
		if got != collection {
			t.Fatalf("selection not saved: %q", got)
		}
	}
	if err := ms.SetConfig(ctx, cfgVoiceRAGCollection, public); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"u42--private", "u43--mismatched", "g999999--orphan", "voice--missing", "legacy"} {
		w := request(admin, "PUT", "/api/voice/kb", fmt.Sprintf(`{"collection":%q}`, invalid), s.handleVoiceKB)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid selection %q: %d", invalid, w.Code)
		}
		got, _, _ := ms.GetConfig(ctx, cfgVoiceRAGCollection)
		if got != public {
			t.Fatal("rejected selection changed the active collection")
		}
	}
	w := request(serviceUser, "POST", "/api/voice/search", `{"query":"When are you open?"}`, s.handleVoiceSearch)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Public opening hours") || embeddings.Load() != 1 {
		t.Fatalf("gateway search failed: %d %s", w.Code, w.Body.String())
	}
	for _, legacy := range []string{"u42--private", "g999999--orphan", "voice--missing", "u43--mismatched"} {
		if err := ms.SetConfig(ctx, cfgVoiceRAGCollection, legacy); err != nil {
			t.Fatal(err)
		}
		w = request(serviceUser, "POST", "/api/voice/search", `{"query":"private content"}`, s.handleVoiceSearch)
		if w.Code != http.StatusServiceUnavailable || embeddings.Load() != 1 {
			t.Fatalf("legacy invalid selection reached search: %q, %d, embeddings=%d", legacy, w.Code, embeddings.Load())
		}
	}
	if err := ms.SetConfig(ctx, cfgVoiceRAGCollection, groupCollection); err != nil {
		t.Fatal(err)
	}
	if err := ms.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	w = request(serviceUser, "POST", "/api/voice/search", `{"query":"old group content"}`, s.handleVoiceSearch)
	if w.Code != http.StatusServiceUnavailable || embeddings.Load() != 1 {
		t.Fatal("deleted group's collection remained available to callers")
	}
}
