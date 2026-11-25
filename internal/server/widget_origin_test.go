package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"

	"prism/internal/memory"
)

func TestWidgetWebSocketProxyKeepsServiceSessionWithoutReplacingPrismCookies(t *testing.T) {
	s := isolatedWidgetServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Cookie"), widgetCookie+"=") || strings.Contains(r.Header.Get("Cookie"), sessionCookie+"=") || r.URL.Query().Get("widget_grant") != "" {
			http.Error(w, "unexpected Prism credential", 403)
			return
		}
		headers := http.Header{}
		headers.Add("Set-Cookie", sessionCookie+"=rejected; Path=/; HttpOnly")
		headers.Add("Set-Cookie", widgetCookie+"=rejected; Path=/; HttpOnly")
		headers.Add("Set-Cookie", "service_session=preserved; Path=/; HttpOnly")
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, headers)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, data, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(kind, data)
		}
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	s.cfg.WorkspaceDial = func(ctx context.Context, _ int) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", target.Host)
	}
	handler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	content := httptest.NewServer(handler)
	defer content.Close()
	grant := widgetGrantForTest(t, s, "")
	headers := http.Header{"Origin": []string{content.URL}}
	headers.Set("Cookie", widgetCookie+"="+grant+"; service_session=old")
	conn, response, err := (&websocket.Dialer{HandshakeTimeout: 3 * time.Second}).Dial("ws"+strings.TrimPrefix(content.URL, "http")+"/proxy/3000/ws?widget_grant="+url.QueryEscape(grant), headers)
	if err != nil {
		t.Fatalf("service WebSocket upgrade failed: %v", err)
	}
	defer conn.Close()
	serviceCookie := false
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookie || cookie.Name == widgetCookie && cookie.Value != grant {
			t.Fatalf("service upgrade replaced a Prism cookie: %v", response.Header.Values("Set-Cookie"))
		}
		serviceCookie = serviceCookie || cookie.Name == "service_session" && cookie.Value == "preserved"
	}
	if !serviceCookie {
		t.Fatal("service cookie was not preserved")
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("service message é")); err != nil {
		t.Fatal(err)
	}
	_, received, err := conn.ReadMessage()
	if err != nil || string(received) != "service message é" {
		t.Fatalf("service WebSocket message failed: %q %v", received, err)
	}
}

func isolatedWidgetServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "fixture.json"), []byte("fixture data"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(Config{
		WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins"),
		Port: "8080", WidgetListenAddr: ":8081", WidgetOrigin: "https://widgets.prism.test",
		AuthToken: "fixture-dashboard-token", AgentContainer: "prism-workspace",
		WebFS: fstest.MapFS{
			"web/index.html":        &fstest.MapFile{Data: []byte("dashboard fixture")},
			"web/widget-frame.html": &fstest.MapFile{Data: []byte("widget frame fixture")},
			"web/widget-frame.js":   &fstest.MapFile{Data: []byte("widget frame runtime")},
		},
	})
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return s
}

func TestWidgetAccessSurvivesRestartWithTheInstallationKey(t *testing.T) {
	first := isolatedWidgetServer(t)
	first.initializeWidgetKey([]byte("synthetic-installation-private-key"))
	grant := widgetGrantForTest(t, first, "")
	restarted := isolatedWidgetServer(t)
	restarted.initializeWidgetKey([]byte("synthetic-installation-private-key"))
	if _, ok := restarted.openWidgetGrant(grant); !ok {
		t.Fatal("a server restart invalidated an existing widget or service")
	}
	other := isolatedWidgetServer(t)
	other.initializeWidgetKey([]byte("different-installation-private-key"))
	if _, ok := other.openWidgetGrant(grant); ok {
		t.Fatal("another installation accepted the widget credential")
	}
}

func widgetGrantForTest(t *testing.T, s *Server, cookie string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "https://prism.test/api/widget/grant", nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	} else {
		r.Header.Set("Authorization", "Bearer "+s.cfg.AuthToken)
	}
	token, err := s.mintWidgetGrant(r)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func serveWidgetTest(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://widgets.prism.test"+path, nil)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: widgetCookie, Value: token})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestWidgetOriginDoesNotAcceptDashboardCredentialsOrControlRoutes(t *testing.T) {
	s := isolatedWidgetServer(t)
	handler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"cookie", "bearer"} {
		r := httptest.NewRequest(http.MethodGet, "https://widgets.prism.test/data/fixture.json", nil)
		if credential == "cookie" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.cfg.AuthToken})
		} else {
			r.Header.Set("Authorization", "Bearer "+s.cfg.AuthToken)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("dashboard %s authorized content listener: %d", credential, w.Code)
		}
	}
	grant := widgetGrantForTest(t, s, "")
	for _, path := range []string{
		"/api/admin/users", "/api/admin/platform", "/api/user/secrets/FIXTURE",
		"/api/group/secrets?group=1", "/api/email/config", "/api/ai/config",
		"/api/mcp", "/api/widget/grant", "/login", "/settings.html", "/admin",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			w := serveWidgetTest(handler, method, path, grant)
			if w.Code != 403 {
				t.Fatalf("control route exposed through widget: %s %s = %d", method, path, w.Code)
			}
		}
	}
	w := serveWidgetTest(handler, http.MethodGet, "/data/fixture.json", grant)
	if w.Code != 200 || w.Body.String() != "fixture data" {
		t.Fatalf("authorized relative data fetch broken: %d %s", w.Code, w.Body.String())
	}
}

func TestWidgetOriginRejectsTamperingAndCredentialRevocation(t *testing.T) {
	s := isolatedWidgetServer(t)
	handler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	grant := widgetGrantForTest(t, s, "")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(grant, "pw1."))
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	tampered := "pw1." + base64.RawURLEncoding.EncodeToString(raw)
	if w := serveWidgetTest(handler, http.MethodGet, "/data/fixture.json", tampered); w.Code != 401 {
		t.Fatal("modified widget grant accepted", w.Code)
	}
	s.cfg.AuthToken = "rotated-fixture-dashboard-token"
	if w := serveWidgetTest(handler, http.MethodGet, "/data/fixture.json", grant); w.Code != 401 {
		t.Fatal("old deployment credential remained authorized", w.Code)
	}
}

func TestWidgetAuthorizationRejectsOtherOrigins(t *testing.T) {
	s := isolatedWidgetServer(t)
	handler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	grant := widgetGrantForTest(t, s, "")
	for _, origin := range []string{"https://unrelated.invalid", "null", "https://prism.test"} {
		r := httptest.NewRequest(http.MethodPost, "https://widgets.prism.test/__prism_widget/authorize", strings.NewReader("{\"token\":\""+grant+"\"}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 || len(w.Result().Cookies()) != 0 {
			t.Fatalf("cross-origin authorization changed content identity: %s = %d", origin, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "https://widgets.prism.test/__prism_widget/authorize", strings.NewReader("{\"token\":\""+grant+"\"}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://widgets.prism.test")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Name != widgetCookie || !w.Result().Cookies()[0].HttpOnly || !w.Result().Cookies()[0].Secure {
		t.Fatal("same-origin frame authorization or its protected cookie broke", w.Code)
	}
}

func TestWidgetOriginRechecksLoginAndAccountState(t *testing.T) {
	s := isolatedWidgetServer(t)
	ms := securityStore(t)
	s.memStore, s.cfg.MultiUser = ms, true
	user := scopeFixtureUser(t, ms, "widget-owner", memory.RoleMember)
	login := "fixture-widget-login"
	if err := ms.CreateLoginSession(t.Context(), login, user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	grant := widgetGrantForTest(t, s, login)
	if w := serveWidgetTest(handler, http.MethodGet, "/api/profile", grant); w.Code != 200 {
		t.Fatal("signed-in user's widget denied", w.Code, w.Body.String())
	}
	if err := ms.DeleteLoginSession(t.Context(), login); err != nil {
		t.Fatal(err)
	}
	if w := serveWidgetTest(handler, http.MethodGet, "/api/profile", grant); w.Code != 401 {
		t.Fatal("logged-out widget session remained authorized", w.Code)
	}
	if err := ms.CreateLoginSession(t.Context(), login, user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetUserStatus(t.Context(), user.ID, memory.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if w := serveWidgetTest(handler, http.MethodGet, "/api/profile", grant); w.Code != 401 {
		t.Fatal("disabled user's widget session remained authorized", w.Code)
	}
}

func TestWidgetOriginCanonicalPathsCannotReachControlRoutes(t *testing.T) {
	s := isolatedWidgetServer(t)
	handler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	grant := widgetGrantForTest(t, s, "")
	for _, original := range []string{
		"/data/../api/admin/users",
		"/data/%2e%2e/api/admin/users",
		"/data/%2E%2E%2Fapi%2Fadmin%2Fusers",
		"/data//../../api/admin/users",
		"/proxy/../api/admin/users",
		"//api/admin/users",
	} {
		p := original
		var w *httptest.ResponseRecorder
		for redirects := 0; redirects < 4; redirects++ {
			w = serveWidgetTest(handler, http.MethodGet, p, grant)
			if w.Code < 300 || w.Code >= 400 {
				break
			}
			next, err := url.Parse(w.Header().Get("Location"))
			if err != nil || next.IsAbs() {
				t.Fatalf("unexpected external redirect for %q: %s", original, w.Header().Get("Location"))
			}
			p = next.String()
		}
		if w.Code != 403 && w.Code != 404 && w.Code != 400 {
			t.Fatalf("canonical path reached a different handler: %s = %d %s", original, w.Code, w.Body.String())
		}
	}
}

func TestDashboardGeneratedDocumentsRedirectToContentOrigin(t *testing.T) {
	s := isolatedWidgetServer(t)
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/plugins/default/fixture.html", "/data/fixture.svg",
		"/data/fixture.xhtml", "/data/no-extension", "/proxy/3000/",
	} {
		r := httptest.NewRequest(http.MethodGet, "https://prism.test"+path, nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.cfg.AuthToken})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		location, err := url.Parse(w.Header().Get("Location"))
		if err != nil || w.Code != http.StatusTemporaryRedirect || location.Host != "widgets.prism.test" || location.Path != r.URL.Path {
			t.Fatalf("active document stayed on dashboard origin: %s %d %s", path, w.Code, w.Header().Get("Location"))
		}
		if _, ok := s.openWidgetGrant(location.Query().Get("widget_grant")); !ok {
			t.Fatal("redirect did not carry a valid content grant")
		}
	}
	r := httptest.NewRequest(http.MethodPost, "https://prism.test/__prism_widget/authorize", strings.NewReader("{}"))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.cfg.AuthToken})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code >= 200 && w.Code < 300 {
		t.Fatal("widget document authorization unexpectedly available on dashboard origin")
	}
}

func TestWidgetProxyKeepsServiceStateWithoutForwardingPrismCredentials(t *testing.T) {
	s := isolatedWidgetServer(t)
	var received struct{ Authorization, Cookie, Query, Body string }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received.Authorization, received.Cookie = r.Header.Get("Authorization"), r.Header.Get("Cookie")
		received.Query, received.Body = r.URL.RawQuery, string(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	s.cfg.WorkspaceDial = func(ctx context.Context, port int) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", target.Host)
	}
	handler, err := s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	grant := widgetGrantForTest(t, s, "")
	r := httptest.NewRequest(http.MethodPost, "https://widgets.prism.test/proxy/3000/fixture?value=keep&widget_grant="+url.QueryEscape(grant), strings.NewReader("fixture service input"))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "unrelated-main-cookie"})
	r.AddCookie(&http.Cookie{Name: widgetCookie, Value: "unrelated-widget-cookie"})
	r.AddCookie(&http.Cookie{Name: "service_session", Value: "keep"})
	r.Header.Set("Authorization", "Bearer unrelated-main-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("service proxy failed: %d %s", w.Code, w.Body.String())
	}
	query, _ := url.ParseQuery(received.Query)
	if received.Authorization != "" || received.Cookie != "service_session=keep" || query.Get("widget_grant") != "" || query.Get("value") != "keep" || received.Body != "fixture service input" {
		b, _ := json.Marshal(received)
		t.Fatalf("proxy leaked Prism credentials or lost service state: %s", b)
	}
}
