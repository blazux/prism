package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/email"
	"prism/internal/memory"
)

func TestEmailAccountGuardsAndCategoriesAcrossReconfiguration(t *testing.T) {
	store := securityStore(t)
	s := &Server{memStore: store}
	u, other := &memory.User{ID: 7}, &memory.User{ID: 8}
	oldConfig := `{"imap_host":"never-contact.invalid","smtp_host":"smtp.invalid","user":"old@example.test"}`
	for _, scope := range []string{"u7", "u8"} {
		if err := store.ConfigScope(scope).SetConfig(t.Context(), emailConfigKey, oldConfig); err != nil {
			t.Fatal(err)
		}
	}
	store.ConfigScope("u7").SetConfig(t.Context(), "email_tags", `{"1":{"category":"User seven"}}`)
	store.ConfigScope("u8").SetConfig(t.Context(), "email_tags", `{"1":{"category":"User eight"}}`)
	request := func(user *memory.User, method, path, body string, handler http.HandlerFunc, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler(w, withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user))
		if w.Code != status {
			t.Fatalf("%s %s = %d: %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	readAccount := func(user *memory.User) string {
		t.Helper()
		w := request(user, "GET", "/api/email/config", "", s.handleEmailConfig, 200)
		var cfg struct {
			Account string `json:"account"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil || cfg.Account == "" {
			t.Fatal("configuration did not expose a non-secret mailbox fingerprint", err)
		}
		return cfg.Account
	}
	old := readAccount(u)
	// A Settings write must bind existing legacy categories before changing boxes.
	request(u, "POST", "/api/email/config", `{"user":"new@example.test"}`, s.handleEmailConfig, 200)
	current := readAccount(u)
	if old == current {
		t.Fatal("changed mailbox retained the same identity")
	}
	w := request(u, "GET", "/api/email/tags?account="+current, "", s.handleEmailTags, 200)
	if strings.Contains(w.Body.String(), "User seven") {
		t.Fatal("new mailbox inherited old UID categories")
	}
	request(u, "POST", "/api/email/tags?account="+current, `{"1":{"category":"New mailbox"}}`, s.handleEmailTags, 200)
	for _, check := range []struct {
		path, body string
		method     string
		handler    http.HandlerFunc
	}{
		{"/api/email/read?uid=1&account=" + old, "", "GET", s.handleEmailRead},
		{"/api/email/attachment?uid=1&i=0&account=" + old, "", "GET", s.handleEmailAttachment},
		{"/api/email/status?account=" + old, "", "GET", s.handleEmailStatus},
		{"/api/email/markseen", `{"uid":1,"seen":true,"account":"` + old + `"}`, "POST", s.handleEmailMarkSeen},
		{"/api/email/drafts", `{"client_id":"mailbox-guard-fixture","to":"recipient@example.test","body":"Keep my draft","account":"` + old + `"}`, "POST", s.handleEmailDrafts},
	} {
		request(u, check.method, check.path, check.body, check.handler, 409)
	}
	drafts, err := store.ConfigScope("u7").ListEmailDrafts(t.Context())
	if err != nil || len(drafts) != 0 {
		t.Fatal("stale draft was created against the replacement mailbox", drafts, err)
	}
	request(u, "POST", "/api/email/config", oldConfig, s.handleEmailConfig, 200)
	w = request(u, "GET", "/api/email/tags", "", s.handleEmailTags, 200)
	if !strings.Contains(w.Body.String(), "User seven") || strings.Contains(w.Body.String(), "New mailbox") {
		t.Fatal("original mailbox categories did not return", w.Body.String())
	}
	w = request(other, "GET", "/api/email/tags", "", s.handleEmailTags, 200)
	if !strings.Contains(w.Body.String(), "User eight") || strings.Contains(w.Body.String(), "User seven") {
		t.Fatal("categories escaped the user's scope", w.Body.String())
	}
	// Guard and fingerprint remain optional for older callers and use no secret.
	if !emailAccountMatches(httptest.NewRecorder(), email.Config{}, "") {
		t.Fatal("legacy callers require an unnecessary new parameter")
	}
}

func TestEmailAccountConflictHTTPContract(t *testing.T) {
	store := securityStore(t)
	owner := scopeFixtureUser(t, store, "mailbox-conflict-owner", memory.RoleMember)
	s := New(Config{MultiUser: true, WorkspaceDir: t.TempDir(), PluginDir: t.TempDir()})
	s.memStore = store
	if err := store.ConfigScope(fmt.Sprintf("u%d", owner.ID)).SetConfig(t.Context(), emailConfigKey,
		`{"imap_host":"never-contact.invalid","smtp_host":"never-contact.invalid","user":"current@example.test"}`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, errorText string
		status                int
	}{
		{"stale mailbox", `{"action":"move","uid":1,"account":"previous-mailbox"}`, email.ErrAccountChanged.Error(), http.StatusConflict},
		{"legacy ordinary validation", `{"action":"move"}`, "uid", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := withUser(httptest.NewRequest(http.MethodPost, "/api/builtin/email?session=mailbox-fixture", strings.NewReader(tc.body)), owner)
			s.handleBuiltinTool(w, r)
			if w.Code != tc.status {
				t.Fatalf("HTTP status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if _, ok := response["result"]; !ok || !strings.Contains(string(response["error"]), tc.errorText) {
				t.Fatalf("tool error response contract changed: %s", w.Body.String())
			}
		})
	}
}
