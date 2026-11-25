package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"prism/internal/memory"
)

type unreadAuthenticationBody struct{ reads int }

func (b *unreadAuthenticationBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("authentication body must not be read")
}

func authMethodHandler(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/signup", s.handleSignup)
	return s.withAuth(mux)
}

func TestAuthenticationRejectsUnsupportedMethodsBeforeReadingBodyOrStore(t *testing.T) {
	s := &Server{cfg: Config{MultiUser: true}}
	handler := authMethodHandler(s)
	for _, path := range []string{"/api/login", "/api/signup"} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			t.Run(path+"/"+method, func(t *testing.T) {
				body := &unreadAuthenticationBody{}
				r := httptest.NewRequest(method, "http://prism.local"+path, body)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
					t.Fatalf("unsupported method reached authentication: %d %s", w.Code, w.Body.String())
				}
				if body.reads != 0 || len(w.Result().Cookies()) != 0 {
					t.Fatal("unsupported method read credentials or issued a cookie")
				}
			})
		}
	}
}

func TestAuthenticationMethodChangesCannotBypassAttemptLimits(t *testing.T) {
	ms := securityStore(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ms.CreateUser(t.Context(), "methods@example.test", string(hash), "Fixture", memory.RoleMember, memory.StatusApproved); err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, cfg: Config{MultiUser: true}}
	handler := authMethodHandler(s)
	request := func(method, path, body, address string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://prism.local"+path, strings.NewReader(body))
		r.RemoteAddr = address
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	address := "192.0.2.20:1234"
	for i := 0; i < 30; i++ {
		if w := request(http.MethodPost, "/api/login", `{"email":"methods@example.test","password":"wrong-password"}`, address); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	login := `{"email":"methods@example.test","password":"fixture-password"}`
	if w := request(http.MethodPost, "/api/login", login, address); w.Code != http.StatusTooManyRequests {
		t.Fatalf("login limit missing: %d", w.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead} {
		if w := request(method, "/api/login", login, address); w.Code != http.StatusMethodNotAllowed || len(w.Result().Cookies()) != 0 {
			t.Fatalf("method %s bypassed login limit: %d", method, w.Code)
		}
	}
	if w := request(http.MethodPost, "/api/login", login, "192.0.2.21:1234"); w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 {
		t.Fatalf("normal login stopped working: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 10; i++ {
		if w := request(http.MethodPost, "/api/signup", "{}", address); w.Code != http.StatusBadRequest {
			t.Fatalf("signup attempt %d: %d", i, w.Code)
		}
	}
	signup := `{"email":"new-methods@example.test","password":"fixture-password","displayName":"New fixture"}`
	if w := request(http.MethodPost, "/api/signup", signup, address); w.Code != http.StatusTooManyRequests {
		t.Fatalf("signup limit missing: %d", w.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if w := request(method, "/api/signup", signup, address); w.Code != http.StatusMethodNotAllowed || len(w.Result().Cookies()) != 0 {
			t.Fatalf("method %s bypassed signup limit: %d", method, w.Code)
		}
	}
	if _, _, err := ms.GetUserByEmail(t.Context(), "new-methods@example.test"); err == nil {
		t.Fatal("unsupported signup method created an account")
	}
}
