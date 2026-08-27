package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"prism/internal/memory"
)

func srvWithToken(tok string) *Server { return &Server{cfg: Config{AuthToken: tok}} }

func TestCapTokenRoundTrip(t *testing.T) {
	s := srvWithToken("deploy-secret")
	for _, tc := range []struct {
		uid     int64
		session string
	}{
		{0, "default"},
		{0, "room-g3"},
		{42, "u42-main"},
		{7, "u7-work board"},
	} {
		tok := mintCapToken(s.capKey(), tc.uid, tc.session)
		uid, session, ok := s.verifyCapToken(tok)
		if !ok || uid != tc.uid || session != tc.session {
			t.Errorf("round-trip (%d,%q): got (%d,%q,%v)", tc.uid, tc.session, uid, session, ok)
		}
	}
}

func TestCapTokenIsStableAcrossRestart(t *testing.T) {
	// Same deployment token → same key → a cron's baked token still verifies
	// after a restart (docs/UPGRADING.md rule 4).
	a, b := srvWithToken("deploy-secret"), srvWithToken("deploy-secret")
	tok := a.selfCallToken("u42-main")
	if _, _, ok := b.verifyCapToken(tok); !ok {
		t.Error("token minted before restart does not verify after")
	}
}

func TestCapTokenRejectsTampering(t *testing.T) {
	s := srvWithToken("deploy-secret")
	good := mintCapToken(s.capKey(), 42, "u42-main")

	// A different deployment token must not verify it (forged key).
	other := srvWithToken("other-secret")
	if _, _, ok := other.verifyCapToken(good); ok {
		t.Error("token verified under a different deployment token")
	}
	// Flipped payload, same signature.
	forged := mintCapToken(s.capKey(), 42, "u42-main")
	forged = forged[:len(capTokenPrefix)] + "X" + forged[len(capTokenPrefix)+1:]
	if _, _, ok := s.verifyCapToken(forged); ok {
		t.Error("tampered payload verified")
	}
	// The plain deployment token is not a capability token.
	if _, _, ok := s.verifyCapToken("deploy-secret"); ok {
		t.Error("deployment token accepted as capability token")
	}
	if _, _, ok := s.verifyCapToken("pc1.garbage"); ok {
		t.Error("malformed token accepted")
	}
}

func TestCapTokenDisabledWithoutAuth(t *testing.T) {
	// Auth off → capability tokens disabled, $PRISM_TOKEN stays empty (old behaviour).
	s := srvWithToken("")
	if s.selfCallToken("u42-main") != "" {
		t.Error("expected empty self-call token when auth is disabled")
	}
	if _, _, ok := s.verifyCapToken("pc1.anything.sig"); ok {
		t.Error("verify should fail when auth is disabled")
	}
}

func TestCapTokenFromRequest(t *testing.T) {
	s := srvWithToken("deploy-secret")
	tok := s.selfCallToken("u42-main")

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if got := capTokenFromRequest(r); got != tok {
		t.Error("bearer capability token not extracted")
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	if got := capTokenFromRequest(r2); got != tok {
		t.Error("cookie capability token not extracted")
	}
	// The deployment token is not a capability token.
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.Header.Set("Authorization", "Bearer deploy-secret")
	if got := capTokenFromRequest(r3); got != "" {
		t.Error("deployment bearer must not be read as a capability token")
	}
}

func TestUserForCapTokenServiceIdentity(t *testing.T) {
	s := srvWithToken("deploy-secret")
	// uid 0 → the unrestricted service identity, exactly as the deployment
	// token resolved. Single-user's whole world is this path.
	if u := s.userForCapToken(nil, 0); u != serviceUser {
		t.Errorf("uid 0 should resolve to serviceUser, got %v", u)
	}
	if !serviceUser.IsGlobalAdmin() {
		t.Error("serviceUser must stay global admin (single-user unchanged)")
	}
	_ = memory.StatusApproved
}
