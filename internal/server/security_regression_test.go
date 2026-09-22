package server

import (
	"net/http"
	"net/http/httptest"
	"prism/internal/memory"
	"strings"
	"testing"
	"time"
)

func TestPrivateRoutesRequireLogin(t *testing.T) {
	s := &Server{cfg: Config{AuthToken: "fixture"}}
	next := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, path := range []string{"/data/doc", "/screenshots/image", "/plugins/default/widget.html", "/proxy/service/80/", "/socket.io/"} {
		w := httptest.NewRecorder()
		next.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer fixture")
		w = httptest.NewRecorder()
		next.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("owner denied: %s", path)
		}
	}
	w := httptest.NewRecorder()
	next.ServeHTTP(w, httptest.NewRequest("GET", "/app.js", nil))
	if w.Code != 204 {
		t.Fatal("public application shell broken")
	}
}
func TestOAuthPageEscapesErrors(t *testing.T) {
	w := httptest.NewRecorder()
	oauthDonePage(w, "google", `<script>fixture()</script>`)
	if strings.Contains(w.Body.String(), `<script>fixture()`) {
		t.Fatal("unescaped HTML")
	}
	s := &Server{}
	s.oauthStates.Store("fixture", oauthState{provider: "google", exp: time.Now().Add(time.Minute)})
	r := httptest.NewRequest("GET", "/api/oauth/google/callback?state=fixture&error=%3Cscript%3Efixture()%3C/script%3E", nil)
	w = httptest.NewRecorder()
	s.oauthCallback(w, r, "google")
	if strings.Contains(w.Body.String(), "fixture()") {
		t.Fatal("untrusted provider error reflected")
	}
	if _, ok := s.oauthStates.Load("fixture"); ok {
		t.Fatal("denied authorization did not consume state")
	}
}
func TestBrowserSecurityAndUnavailableStore(t *testing.T) {
	s := &Server{cfg: Config{MultiUser: true}}
	next := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	w := httptest.NewRecorder()
	next.ServeHTTP(w, httptest.NewRequest("GET", "/api/secrets", nil))
	if w.Code != 503 {
		t.Fatal("missing database failed open")
	}
	r := httptest.NewRequest("POST", "http://prism.local/api/login", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://attacker.invalid")
	w = httptest.NewRecorder()
	next.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	r = httptest.NewRequest("POST", "https://prism.local/api/login", nil)
	w = httptest.NewRecorder()
	setSessionCookie(w, "fixture", 60, r)
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie lacks Secure")
	}
	local := &Server{}
	for i := 0; i < 30; i++ {
		if !local.allowAuthentication(r) {
			t.Fatal("early rate limit")
		}
	}
	if local.allowAuthentication(r) {
		t.Fatal("authentication flood not limited")
	}
}
func TestCapabilitiesCannotChangeSession(t *testing.T) {
	s := &Server{cfg: Config{AuthToken: "fixture"}}
	r := httptest.NewRequest("POST", "/api/builtin/read_file", nil)
	scoped, ok := s.capabilityRequest(r, 0, "default")
	if !ok {
		t.Fatal("local capability denied")
	}
	if id, ok := s.sessionFor(scoped, ""); !ok || id != "default" {
		t.Fatal("implicit bound session lost")
	}
	if _, ok := s.sessionFor(scoped, "other"); ok {
		t.Fatal("capability session widened")
	}
	s.cfg.MultiUser = true
	if s.userForCapToken(t.Context(), 0) != nil {
		t.Fatal("unscoped capability grants admin")
	}
	group := &memory.User{ID: -7, Role: memory.RoleMember}
	cc := s.callerContextForUser(t.Context(), group, "room-g7")
	if cc.GlobalAdmin || cc.RAGScope != "g7" || cc.Guard == nil {
		t.Fatal("group context lost")
	}
	for _, path := range []string{"/api/admin/users", "/api/secrets/key", "/api/ai/config", "/api/exec"} {
		if groupCapabilityPath(httptest.NewRequest("POST", path, nil)) {
			t.Fatal("group can reach control plane", path)
		}
	}
}
func TestPluginOwnership(t *testing.T) {
	s := &Server{cfg: Config{MultiUser: true}}
	r := withUser(httptest.NewRequest("GET", "/plugins/u2-main/widget.html", nil), &memory.User{ID: 1, Role: memory.RoleMember})
	if s.canReadPluginSession(r, "u2-main") {
		t.Fatal("foreign widget readable")
	}
	if !s.canReadPluginSession(r, "u1-main") {
		t.Fatal("own widget refused")
	}
}
