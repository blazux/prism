package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"prism/internal/memory"
)

func TestVoxPairingCredentialCannotBecomeAnAdminOrBrowserToken(t *testing.T) {
	for _, multi := range []bool{false, true} {
		s := New(Config{MultiUser: multi, AuthToken: "deployment-fixture"})
		s.voxState.Store(&voxConnection{Enabled: true, GatewayToken: "phone-fixture"})
		allowed := []string{"/api/voice/capabilities", "/api/voice/caller?number=123", "/api/voice/directory", "/api/voice/search", "/api/voice/inference/v1/models", "/api/voice/inference/v1/chat/completions", "/ws?channel=voice"}
		for _, path := range allowed {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", "Bearer phone-fixture")
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !isServiceIdentity(currentUser(r)) {
					t.Error("gateway identity lost")
				}
				w.WriteHeader(204)
			})).ServeHTTP(w, r)
			if w.Code != 204 {
				t.Errorf("multi=%v gateway refused on %s: %d", multi, path, w.Code)
			}
		}
		for _, path := range []string{"/api/voice", "/api/voice/kb", "/api/vox-connection", "/api/ai/config", "/api/secrets", "/api/builtin/exec_command", "/ws", "/ws?channel=web"} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", "Bearer phone-fixture")
			if s.voxGatewayAuthed(r) {
				t.Errorf("gateway accepted %s", path)
			}
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("gateway escaped onto %s", path) })).ServeHTTP(w, r)
			if w.Code == 200 || w.Code == 204 {
				t.Errorf("gateway escaped onto %s", path)
			}
		}
	}
}

func TestVoxConnectionSecretsPatchRestartAndFailedPairing(t *testing.T) {
	ms := securityStore(t)
	var remote map[string]any
	var reject bool
	vox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "operator" || password != "phone-password-fixture" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PUT" {
			if reject {
				w.WriteHeader(503)
				return
			}
			if json.NewDecoder(r.Body).Decode(&remote) != nil {
				t.Error("invalid pairing payload")
			}
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			json.NewEncoder(w).Encode(map[string]bool{"connected": true})
			return
		}
		json.NewEncoder(w).Encode(remote)
	}))
	defer vox.Close()
	s := New(Config{MultiUser: true, AuthToken: "master-key-fixture"})
	s.memStore = ms
	request := func(method, body string) *httptest.ResponseRecorder {
		r := withUser(httptest.NewRequest(method, "/api/vox-connection", strings.NewReader(body)), serviceUser)
		w := httptest.NewRecorder()
		s.handleVoxConnection(w, r)
		return w
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		r := withUser(httptest.NewRequest(method, "/api/vox-connection", nil), &memory.User{ID: 99, Role: memory.RoleMember})
		w := httptest.NewRecorder()
		s.handleVoxConnection(w, r)
		if w.Code != 403 {
			t.Fatalf("member accepted: %d", w.Code)
		}
	}
	body, _ := json.Marshal(map[string]any{"url": vox.URL, "user": "operator", "password": "phone-password-fixture", "prism_url": "http://prism.test", "model": "default"})
	w := request("PUT", string(body))
	if w.Code != 200 {
		t.Fatalf("pair: %d %s", w.Code, w.Body)
	}
	first := s.voxConnection()
	if first.GatewayToken == "" || first.GatewayToken == s.cfg.AuthToken || remote["prism_token"] != first.GatewayToken {
		t.Fatal("pairing did not use a separate gateway credential")
	}
	for _, secret := range []string{first.GatewayToken, "phone-password-fixture", "master-key-fixture", "gateway_token", "prism_token"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("public response contains %s", secret)
		}
	}
	if !memory.IsIntegrationSecret(voxConnectionSecret) {
		t.Fatal("connection secret could be exported to scripts")
	}
	w = request("PUT", `{"model":"primary::phone"}`)
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if got := s.voxConnection(); got.URL != first.URL || got.Password != first.Password || got.PrismURL != first.PrismURL || got.Model != "primary::phone" {
		t.Fatal("model edit changed connection settings")
	}
	restarted := New(Config{})
	restarted.memStore = ms
	restarted.loadVoxConnection(t.Context())
	if !reflect.DeepEqual(restarted.voxConnection(), s.voxConnection()) {
		t.Fatal("connection not restored after restart")
	}
	reject = true
	w = request("PUT", `{"model":"primary::rejected"}`)
	if w.Code != 502 || s.voxConnection().Model != "primary::phone" {
		t.Fatal("failed remote save replaced working settings")
	}
	reject = false
	w = request("DELETE", "")
	if w.Code != 200 || s.voxURL() != "" || remote["enabled"] != false {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/api/voice/capabilities", nil)
	r.Header.Set("Authorization", "Bearer "+first.GatewayToken)
	if s.voxGatewayAuthed(r) {
		t.Fatal("disconnected credential remains valid")
	}
	w = request("PUT", `{}`)
	if w.Code != 200 || s.voxConnection().GatewayToken == first.GatewayToken {
		t.Fatalf("reconnection reused a revoked gateway credential: %d %s", w.Code, w.Body)
	}
	if s.voxGatewayAuthed(r) {
		t.Fatal("old gateway credential was reactivated")
	}
}

func TestExplicitDisconnectStopsLegacyPhoneCallbacksWithoutRevokingOtherIntegrations(t *testing.T) {
	ms := securityStore(t)
	for _, multi := range []bool{false, true} {
		s := New(Config{MultiUser: multi, AuthToken: "legacy-master-fixture", VoxURL: "http://old-phone.test"})
		s.memStore = ms
		s.voxState.Store(&voxConnection{Enabled: false, URL: "http://old-phone.test"})
		for _, path := range []string{"/api/voice/capabilities", "/api/voice/directory", "/api/voice/caller?number=123", "/ws?channel=voice", "/api/secrets"} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", "Bearer legacy-master-fixture")
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			want := 503
			if path == "/api/secrets" {
				want = 204
			}
			if w.Code != want {
				t.Errorf("multi=%v %s returned %d expected %d", multi, path, w.Code, want)
			}
		}
	}
}

func TestVoxDraftNeverReusesAnotherEndpointSecret(t *testing.T) {
	s := New(Config{})
	s.voxState.Store(&voxConnection{Enabled: true, URL: "http://old.test", User: "operator", Password: "private"})
	for _, body := range []string{`{"url":"http://other.test"}`, `{"user":"someone"}`} {
		c, err := s.readVoxConnectionDraft(httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if err != nil || c.Password != "" {
			t.Fatal("old password forwarded to another endpoint/account")
		}
	}
	for _, bad := range []string{"file:///etc/passwd", "http://user:password@phone.test", "http://phone.test?token=x", "http://phone.test/#fragment"} {
		if _, err := normalizeVoxURL(bad, false); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
