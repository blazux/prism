package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"prism/internal/memory"
	webassets "prism/web"
)

// TestPreferencesBrowserFixture serves real preference/auth handlers over a
// disposable database for opt-in browser tests. Credentials and data are synthetic.
func TestPreferencesBrowserFixture(t *testing.T) {
	ready := os.Getenv("PRISM_PREFERENCES_BROWSER_READY")
	if ready == "" {
		t.Skip("launched by the preferences backend browser test")
	}
	ms := securityStore(t)
	password := "synthetic-fixture-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var accounts []map[string]string
	root := t.TempDir()
	content := httptest.NewUnstartedServer(nil)
	s := New(Config{MultiUser: true, AuthToken: "synthetic-preferences-service", WebFS: widgetBrowserWebFS{webassets.Files()},
		WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins"), WidgetListenAddr: content.Listener.Addr().String(),
		WidgetOrigin: "http://" + content.Listener.Addr().String()})
	s.memStore = ms
	for _, name := range []string{"first", "second"} {
		email := name + "@preferences-fixture.test"
		u, err := ms.CreateUser(t.Context(), email, string(hash), name, memory.RoleMember, memory.StatusApproved)
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, map[string]string{"email": email, "password": password, "owner": s.preferenceOwner(u), "id": strconv.FormatInt(u.ID, 10)})
	}
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__preferences_fixture" || r.URL.Path == "/__preferences_fixture_host" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `<!doctype html><html><head><script src="/preferences.js"></script><script src="/theme.js"></script><script src="/tool-preferences.js"></script></head><body><h1>Synthetic preferences fixture</h1></body></html>`)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	content.Config.Handler, err = s.WidgetHandler()
	if err != nil {
		t.Fatal(err)
	}
	content.Start()
	defer content.Close()
	encoded, err := json.Marshal(map[string]any{"main": server.URL, "widget": content.URL, "accounts": accounts})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ready+".tmp", encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ready+".tmp", ready); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	timeout := time.NewTimer(10 * time.Minute)
	defer ticker.Stop()
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(ready + ".stop"); err == nil {
				return
			}
		case <-timeout.C:
			t.Fatal("preferences browser fixture was not stopped")
		}
	}
}
