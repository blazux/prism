package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"prism/internal/memory"
)

type preferenceResponse struct {
	Values              map[string]string `json:"values"`
	Owner               string            `json:"owner"`
	LegacyImportAllowed bool              `json:"legacyImportAllowed"`
}

func preferenceRequest(s *Server, u *memory.User, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if u != nil {
		r = withUser(r, u)
	}
	w := httptest.NewRecorder()
	s.handlePreferences(w, r)
	return w
}

func preferenceBody(owner string, values map[string]string) string {
	raw, _ := json.Marshal(map[string]any{"owner": owner, "values": values})
	return string(raw)
}

func readPreferenceResponse(t *testing.T, w *httptest.ResponseRecorder) preferenceResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("preferences status %d: %s", w.Code, w.Body)
	}
	var result preferenceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUIPreferenceValidation(t *testing.T) {
	valid := map[string]string{
		"prism-theme": "custom-17300", "prism-custom-themes": `[{"id":"custom-1","label":"Été 🌈","vars":{"--bg":"#ffeedd","color-scheme":"light"}}]`,
		"prism-ui-scale": "125", "prism-density": "comfortable", "prism-bgfx": "0", "prism-ui-font": "'IBM Plex Sans', sans-serif",
		"prism-chat-side": "left", "prism-chat-default": "closed", "prism-window-snap": "1", "active-workspace": "u7-finance",
		"prism-workspace-icons": `{"u7-finance":"🤑","default":"briefcase"}`, "chat-drawer-width": "476.25px",
		"prism-cal-mode": "agenda", "prism-pinned-notes": `["42","vault/note.md"]`, "agent-config": `{"disabled":["exec_command","mcp_tool"],"combinedToolNotices":["widget"]}`,
		"prism-approval":    "manual",
		"prism-admin-group": "42", "vox-supervision-session": "sup-17300-abcd",
	}
	if len(valid) != len(uiPreferenceKeys) {
		t.Fatal("test must cover every native preference")
	}
	for key, value := range valid {
		if !validUIPreference(key, value) {
			t.Errorf("valid native preference rejected: %s", key)
		}
	}
	for _, item := range [][2]string{
		{"prism-ui-font", ""}, {"agent-config", "{}"}, {"prism-custom-themes", "[]"}, {"prism-pinned-notes", "[]"}, {"prism-workspace-icons", "{}"},
		{"prism-admin-group", ""},
		{"prism-pinned-notes", `["` + strings.Repeat("nested/", 100) + `note.md"]`},
	} {
		if !validUIPreference(item[0], item[1]) {
			t.Errorf("empty preference rejected: %s", item[0])
		}
	}
	for _, item := range [][2]string{
		{"email_password", "private"}, {"u7:prism-theme", "dark"}, {"prism-theme", ""}, {"active-workspace", "../other"},
		{"prism-ui-scale", "NaN"}, {"prism-bgfx", "101"}, {"prism-density", "unknown"}, {"prism-chat-side", "top"},
		{"prism-chat-default", "true"}, {"prism-window-snap", "yes"}, {"prism-approval", "enabled"}, {"prism-cal-mode", "year"},
		{"prism-admin-group", "-1"}, {"prism-admin-group", "group"}, {"vox-supervision-session", "../other"},
		{"chat-drawer-width", "100vw"}, {"chat-drawer-width", "2px"}, {"prism-ui-font", "line\nbreak"},
		{"prism-pinned-notes", `{"42":true}`}, {"prism-pinned-notes", `[42]`}, {"prism-pinned-notes", `null`},
		{"prism-workspace-icons", `{"x":1}`}, {"agent-config", `{"disabled":"exec"}`}, {"agent-config", `{"api_key":"private"}`},
		{"prism-custom-themes", `[{}]`}, {"prism-custom-themes", `[{"id":"x","vars":{"--bg":1}}]`},
		{"prism-custom-themes", `[{"id":"x","vars":{}},{"id":"x","vars":{}}]`},
	} {
		if validUIPreference(item[0], item[1]) {
			t.Errorf("invalid preference accepted: %s", item[0])
		}
	}
}

func TestUIPreferencesAuthenticationAndContentBoundary(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		user   *memory.User
		method string
		want   int
	}{
		{nil, "GET", 401}, {&memory.User{ID: -3}, "GET", 403}, {serviceUser, "DELETE", 405}, {serviceUser, "GET", 503},
	} {
		w := preferenceRequest(s, tc.user, tc.method, "/api/preferences", "")
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("method=%s identity=%v: status=%d", tc.method, tc.user, w.Code)
		}
	}
	for _, method := range []string{"GET", "PATCH", "POST"} {
		if widgetAPIAllowed(httptest.NewRequest(method, "http://content.test/api/preferences", nil)) {
			t.Errorf("content origin can access preferences with %s", method)
		}
	}
}

func TestUIPreferenceOwnerSeparatesPersonalEnvironments(t *testing.T) {
	a := &Server{cfg: Config{AuthToken: "fixture-first", WorkspaceDir: "/workspace/first"}}
	b := &Server{cfg: Config{AuthToken: "fixture-second", WorkspaceDir: "/workspace/second"}}
	if a.preferenceOwner(serviceUser) == b.preferenceOwner(serviceUser) {
		t.Fatal("personal environments share the same browser write owner")
	}
	for _, server := range []*Server{a, b} {
		owner := server.preferenceOwner(serviceUser)
		if len(owner) != len("owner-")+32 || strings.Contains(owner, "fixture") || strings.Contains(owner, "workspace") {
			t.Fatal("personal environment identifier exposes configuration")
		}
	}
}

func TestUIPreferencesHostedLegacyRequiresExplicitImport(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms, cfg: Config{AuthToken: "fixture-personal-environment"}}
	r := withUser(httptest.NewRequest("GET", "/api/preferences", nil), serviceUser)
	r = r.WithContext(context.WithValue(r.Context(), resourceContextKey{}, s))
	w := httptest.NewRecorder()
	s.handlePreferences(w, r)
	result := readPreferenceResponse(t, w)
	if result.LegacyImportAllowed || result.Owner != s.preferenceOwner(serviceUser) {
		t.Fatal("unattributed browser values would be auto-imported into a Cloud account")
	}
	other := &Server{cfg: Config{AuthToken: "fixture-other-personal-environment"}}
	w = preferenceRequest(s, serviceUser, "PATCH", "/api/preferences", preferenceBody(other.preferenceOwner(serviceUser), map[string]string{"prism-theme": "wrong-owner"}))
	if w.Code != http.StatusConflict {
		t.Fatal("stale personal environment save was accepted", w.Code)
	}
}

func TestUIPreferencesOwnerScopesAndAtomicImport(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	ctx := t.Context()
	// Unrelated config/secrets and another owner must never be returned.
	if err := ms.SetConfig(ctx, "email_password", "not a UI preference"); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetConfig(ctx, "ui_preferences:future-key", "not public"); err != nil {
		t.Fatal(err)
	}
	users := []*memory.User{serviceUser, {ID: 7}, {ID: 8}}
	for i, user := range users {
		owner := s.preferenceOwner(user)
		body := preferenceBody(owner, map[string]string{"prism-theme": fmt.Sprintf("theme-%d", i), "prism-approval": "manual"})
		result := readPreferenceResponse(t, preferenceRequest(s, user, "PATCH", "/api/preferences?session=g99-other", body))
		if result.Owner != owner || result.LegacyImportAllowed != (user.ID == 0) || len(result.Values) != 2 {
			t.Fatalf("scope response: %+v", result)
		}
		imported := preferenceBody(owner, map[string]string{"prism-theme": "stale-browser", "prism-density": "compact"})
		result = readPreferenceResponse(t, preferenceRequest(s, user, "POST", "/api/preferences?session=another", imported))
		if result.Values["prism-theme"] != fmt.Sprintf("theme-%d", i) || result.Values["prism-density"] != "compact" {
			t.Fatalf("import overwrote existing value: %+v", result)
		}
		result = readPreferenceResponse(t, preferenceRequest(s, user, "GET", "/api/preferences", ""))
		if len(result.Values) != 3 || result.Values["prism-theme"] != fmt.Sprintf("theme-%d", i) {
			t.Fatalf("owner isolation lost: %+v", result)
		}
	}
	wrong := preferenceRequest(s, users[2], "PATCH", "/api/preferences", preferenceBody("u7", map[string]string{"prism-theme": "stale-account"}))
	if wrong.Code != 409 {
		t.Fatal("stale account save was accepted", wrong.Code)
	}
	// All import contenders race on the same absent key. Exactly one wins;
	// a concurrent ordinary preference cannot be dropped by a whole-map write.
	user := &memory.User{ID: 99}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			method, values := "POST", map[string]string{"prism-theme": fmt.Sprintf("import-%d", i)}
			if i == 0 {
				method, values = "PATCH", map[string]string{"prism-density": "spacious"}
			}
			results <- preferenceRequest(s, user, method, "/api/preferences", preferenceBody("u99", values))
		}(i)
	}
	wg.Wait()
	close(results)
	var winner string
	for w := range results {
		result := readPreferenceResponse(t, w)
		if theme := result.Values["prism-theme"]; theme != "" {
			if winner != "" && winner != theme {
				t.Fatal("concurrent import overwrote winner")
			}
			winner = theme
		}
	}
	result := readPreferenceResponse(t, preferenceRequest(s, user, "GET", "/api/preferences", ""))
	if winner == "" || result.Values["prism-theme"] != winner || result.Values["prism-density"] != "spacious" {
		t.Fatalf("concurrent update lost: %+v", result)
	}
}

func TestUIPreferencesRejectInvalidWritesAtomically(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	for _, body := range []string{
		preferenceBody(s.preferenceOwner(serviceUser), map[string]string{"prism-theme": "new", "email_password": "secret"}),
		preferenceBody(s.preferenceOwner(serviceUser), map[string]string{"prism-approval": "yes"}),
		`{"owner":"wrong","values":null}`,
		`{"owner":"wrong","values":{}} {}`,
		`{"owner":"wrong","values":{},"session":"group"}`,
		`{"values":{}}`,
	} {
		w := preferenceRequest(s, serviceUser, "PATCH", "/api/preferences", body)
		if w.Code != 400 && w.Code != 409 {
			t.Fatalf("invalid write status=%d", w.Code)
		}
	}
	w := preferenceRequest(s, serviceUser, "PATCH", "/api/preferences", preferenceBody(s.preferenceOwner(serviceUser), map[string]string{"prism-ui-font": strings.Repeat("x", maxUIPreferenceBytes)}))
	if w.Code != 413 {
		t.Fatal("oversized body accepted", w.Code)
	}
	result := readPreferenceResponse(t, preferenceRequest(s, serviceUser, "GET", "/api/preferences", ""))
	if len(result.Values) != 0 {
		t.Fatal("invalid patch partially persisted")
	}
	// Store-level limit applies to the aggregate, not only the new patch.
	if _, err := ms.SaveUIPreferences(t.Context(), map[string]string{"one": strings.Repeat("x", 70000)}, false, maxUIPreferenceBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.SaveUIPreferences(t.Context(), map[string]string{"two": strings.Repeat("x", 70000)}, false, maxUIPreferenceBytes); err != memory.ErrUIPreferencesTooLarge {
		t.Fatal("aggregate limit bypassed", err)
	}
	stored, err := ms.UIPreferences(t.Context())
	if err != nil || len(stored) != 1 {
		t.Fatal("failed aggregate update was not rolled back", err, stored)
	}
}
