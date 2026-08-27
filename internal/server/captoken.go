package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"prism/internal/memory"
)

// Capability tokens: the value handed to the agent as $PRISM_TOKEN.
//
// The agent's own code — exec_command, custom tools, cron jobs, the widget
// preview browser — calls back into prism-server, and needs a credential to do
// it. That credential used to be the deployment token (s.cfg.AuthToken), which
// auth.go maps to a GLOBAL ADMIN. In a shared deployment that means "run
// `echo $PRISM_TOKEN`" hands any member an admin token: full read/write of
// every other user's sessions, secrets and the admin console (server-review
// finding #2).
//
// A capability token instead binds to exactly one identity — the user and
// session the self-call is acting for — so leaking it grants no more than the
// member already has in that session. Same variable name, same `Bearer`
// header, same system prompt: the agent sees no difference.
//
// It is a stateless HMAC, not a stored random string, on purpose: a cron job
// bakes the literal token into the crontab, and that value must still verify
// after a restart (docs/UPGRADING.md rule 4). The key is derived from the
// deployment token, so it is stable across restarts of the same deployment and
// rotates only when the operator rotates PRISM_TOKEN — at which point a
// hand-written cron using the old literal token would break too, so the
// behaviour matches what operators already expect.
//
// The deployment token itself keeps working exactly as before (still → global
// admin), so an operator's own `curl -H "Authorization: Bearer $PRISM_TOKEN"`
// and any cron created before this change are unaffected. Only what the agent
// is *handed* changes.

const capTokenPrefix = "pc1."

// capKey derives the HMAC key from the deployment token. Returns nil when auth
// is disabled (AuthToken == ""), which disables capability tokens entirely —
// the no-auth path stays byte-identical to before.
func (s *Server) capKey() []byte {
	if s.cfg.AuthToken == "" {
		return nil
	}
	sum := sha256.Sum256([]byte("prism-capability-v1\x00" + s.cfg.AuthToken))
	return sum[:]
}

// selfCallToken returns the $PRISM_TOKEN value to inject for a self-call in the
// given session. The bound user is derived from the session id ("u<id>-…" →
// that user; anything else → 0, the single-user / service identity). Falls back
// to "" when auth is disabled, preserving the old empty-token behaviour.
func (s *Server) selfCallToken(sessionID string) string {
	key := s.capKey()
	if key == nil {
		return ""
	}
	return mintCapToken(key, userIDFromSessionID(sessionID), sessionID)
}

func mintCapToken(key []byte, uid int64, session string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(uid, 10) + ":" + session))
	body := capTokenPrefix + payload
	sig := base64.RawURLEncoding.EncodeToString(hmacSum(key, body))
	return body + "." + sig
}

// verifyCapToken checks a token and returns the identity it binds to. ok is
// false for anything that is not a valid capability token (including the plain
// deployment token, which the caller handles separately).
func (s *Server) verifyCapToken(tok string) (uid int64, session string, ok bool) {
	key := s.capKey()
	if key == nil || !strings.HasPrefix(tok, capTokenPrefix) {
		return 0, "", false
	}
	i := strings.LastIndexByte(tok, '.')
	if i < len(capTokenPrefix) {
		return 0, "", false
	}
	body, sigB64 := tok[:i], tok[i+1:]
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || !hmac.Equal(sig, hmacSum(key, body)) {
		return 0, "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(body, capTokenPrefix))
	if err != nil {
		return 0, "", false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return id, parts[1], true
}

// capTokenFromRequest pulls a capability token from the Authorization bearer or
// the session cookie (the widget-preview browser authenticates by cookie, like
// the deployment token does).
func capTokenFromRequest(r *http.Request) string {
	if b := bearerToken(r); strings.HasPrefix(b, capTokenPrefix) {
		return b
	}
	if c := cookieValue(r, sessionCookie); strings.HasPrefix(c, capTokenPrefix) {
		return c
	}
	return ""
}

func hmacSum(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// userForCapToken resolves the identity a verified capability token grants.
// uid 0 (single-user / service-derived session) keeps the existing service
// identity — unrestricted, exactly as the deployment token was. A real user id
// resolves to that account, so their tool policy and scope apply; a missing or
// unapproved account yields nil (denied).
func (s *Server) userForCapToken(ctx context.Context, uid int64) *memory.User {
	if uid == 0 {
		return serviceUser
	}
	ms := s.store()
	if ms == nil {
		return nil
	}
	u, err := ms.GetUserByID(ctx, uid)
	if err != nil || u == nil || u.Status != memory.StatusApproved {
		return nil
	}
	return u
}
