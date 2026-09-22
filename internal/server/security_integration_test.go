package server

import (
	"bytes"
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"prism/internal/memory"
	"prism/internal/oauthx"
	"strings"
	"sync"
	"testing"
	"time"
)

func securityStore(t *testing.T) *memory.Store {
	t.Helper()
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("security_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema+",public")
	parsed.RawQuery = q.Encode()
	ms, err := memory.NewStore(ctx, parsed.String(), bytes.Repeat([]byte{5}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ms.Close(); db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); db.Close(ctx) })
	return ms
}
func TestSecurityOAuthKeepsInitiatingOwner(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u, err := ms.CreateUser(ctx, "fixture@example.test", "unused", "fixture", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	personal := ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
	if err := oauthx.SaveClient(ctx, personal, "google", "fixture-client", "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","token_type":"Bearer","expires_in":3600}`)
	}))
	defer remote.Close()
	original := oauthx.Providers["google"]
	changed := original
	changed.TokenURL = remote.URL
	oauthx.Providers["google"] = changed
	defer func() { oauthx.Providers["google"] = original }()
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, AuthToken: "fixture"}}
	start := withUser(httptest.NewRequest("GET", "http://prism.local/api/oauth/google/start", nil), u)
	w := httptest.NewRecorder()
	s.handleOAuth(w, start)
	redirect, err := url.Parse(w.Header().Get("Location"))
	if err != nil || redirect.Query().Get("state") == "" {
		t.Fatal("authorization did not start")
	}
	callback := httptest.NewRequest("GET", "http://prism.local/api/oauth/google/callback?code=fixture&state="+redirect.Query().Get("state"), nil)
	w = httptest.NewRecorder()
	s.withAuth(http.HandlerFunc(s.handleOAuth)).ServeHTTP(w, callback)
	if !strings.Contains(w.Body.String(), "Connected") {
		t.Fatal("callback failed")
	}
	if _, ok, err := personal.GetSecret(ctx, "oauth_google_token"); err != nil || !ok {
		t.Fatal("token not stored for initiator")
	}
	if _, ok, _ := ms.GetSecret(ctx, "oauth_google_token"); ok {
		t.Fatal("token leaked into global store")
	}
	w = httptest.NewRecorder()
	s.withAuth(http.HandlerFunc(s.handleOAuth)).ServeHTTP(w, callback)
	if strings.Contains(w.Body.String(), "Connected") {
		t.Fatal("state replay accepted")
	}
}
func TestSecurityGroupCapabilityRestrictions(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	g, err := ms.CreateGroup(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	scope := fmt.Sprintf("g%d", g.ID)
	if err := ms.ConfigScope(scope).SetScriptSecret(ctx, "MY_KEY", "fixture-group"); err != nil {
		t.Fatal(err)
	}
	ms.SetScriptSecret(ctx, "GLOBAL_KEY", "fixture-global")
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, AuthToken: "fixture"}}
	session := "room-" + scope
	token := s.selfCallToken(session)
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.withAuth(http.HandlerFunc(s.handleUserSecretByName)).ServeHTTP(w, r)
		return w
	}
	if w := request("/api/user/secrets/MY_KEY?session=" + session); w.Code != 200 || !strings.Contains(w.Body.String(), "fixture-group") {
		t.Fatal("group's own secret unavailable", w.Code)
	}
	if w := request("/api/user/secrets/GLOBAL_KEY?session=" + session); w.Code != 404 {
		t.Fatal("group can read global key", w.Code)
	}
	for _, path := range []string{"/api/admin/users", "/api/exec", "/api/user/secrets/MY_KEY?session=room-g999"} {
		if w := request(path); w.Code != 401 && w.Code != 403 {
			t.Fatal("group capability widened", path, w.Code)
		}
	}
	if err := ms.DeleteGroup(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if w := request("/api/user/secrets/MY_KEY"); w.Code != 401 {
		t.Fatal("deleted group still authenticated", w.Code)
	}
}
func TestSecurityFirstAdminIsAtomic(t *testing.T) {
	ms := securityStore(t)
	var wg sync.WaitGroup
	users := make(chan *memory.User, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u, err := ms.RegisterUser(t.Context(), fmt.Sprintf("fixture%d@example.test", i), "unused", "fixture")
			if err != nil {
				errs <- err
				return
			}
			users <- u
		}(i)
	}
	wg.Wait()
	close(users)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	admins := 0
	for u := range users {
		if u.IsGlobalAdmin() {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("got %d initial admins", admins)
	}
}
