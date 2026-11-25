package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Widget documents run on a content origin, never on the dashboard origin.
// The credential below is encrypted, expires with the login lifetime and is accepted only by the
// content gateway. The original login is checked again on every request, so
// logout, account suspension and permission changes still take effect.
const widgetCookie = "prism_widget"
const widgetGrantTTL = sessionTTL
const widgetAAD = "prism-widget-access-v1"

type widgetCredential struct {
	Kind    string `json:"kind"`
	Value   string `json:"value,omitempty"`
	Origin  string `json:"origin"`
	Expires int64  `json:"expires"`
}

func (s *Server) widgetAEAD() cipher.AEAD {
	s.widgetKeyOnce.Do(func() {
		if s.cfg.AuthToken != "" {
			key := sha256.Sum256([]byte(widgetAAD + "\x00" + s.cfg.AuthToken))
			s.widgetKey = key[:]
		} else {
			s.widgetKey = make([]byte, 32)
			if _, err := rand.Read(s.widgetKey); err != nil {
				panic("widget encryption key: " + err.Error())
			}
		}
	})
	block, _ := aes.NewCipher(s.widgetKey)
	aead, _ := cipher.NewGCM(block)
	return aead
}

// Use the installation's private key so standalone widgets and proxied apps
// keep working across server restarts. Rotating the deployment token also
// rotates this separate encryption context. No key enters the workspace.
func (s *Server) initializeWidgetKey(privateKey []byte) {
	s.widgetKeyOnce.Do(func() {
		h := sha256.New()
		h.Write([]byte(widgetAAD + "\x00" + s.cfg.AuthToken + "\x00"))
		h.Write(privateKey)
		s.widgetKey = h.Sum(nil)
	})
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) widgetOrigin(r *http.Request) string {
	if s.cfg.WidgetOrigin != "" {
		return strings.TrimRight(s.cfg.WidgetOrigin, "/")
	}
	port := s.cfg.WidgetPublicPort
	if port == "" {
		port = "48082"
	}
	// Internal preview browsers use the container's own listener, rather than
	// the host-published Docker port.
	host := r.Host
	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		if p == s.cfg.Port {
			if _, inner, err := net.SplitHostPort(s.cfg.WidgetListenAddr); err == nil {
				port = inner
			}
		}
	}
	u, _ := url.Parse(requestOrigin(r))
	return u.Scheme + "://" + net.JoinHostPort(strings.Trim(host, "[]"), port)
}

func (s *Server) widgetHosting(r *http.Request) map[string]string {
	if s.cfg.WidgetFrameURL != "" {
		return map[string]string{"frameURL": s.cfg.WidgetFrameURL, "grantURL": s.cfg.WidgetGrantURL}
	}
	if s.cfg.WidgetListenAddr == "" {
		return nil
	}
	origin := s.widgetOrigin(r)
	if origin == requestOrigin(r) {
		return nil
	}
	return map[string]string{
		"frameURL": origin + "/widget-frame.html?parent=" + url.QueryEscape(requestOrigin(r)),
		"grantURL": "/api/widget/grant",
	}
}

func (s *Server) mintWidgetGrant(r *http.Request) (string, error) {
	c := widgetCredential{Origin: requestOrigin(r), Expires: time.Now().Add(widgetGrantTTL).Unix()}
	if token := bearerToken(r); token != "" {
		c.Kind, c.Value = "bearer", token
	} else if token := cookieValue(r, sessionCookie); token != "" {
		c.Kind, c.Value = "cookie", token
	} else if !s.cfg.MultiUser && s.cfg.AuthToken == "" {
		c.Kind = "anonymous"
	} else {
		return "", fmt.Errorf("a current login is required")
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	aead := s.widgetAEAD()
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "pw1." + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, data, []byte(widgetAAD))), nil
}

func (s *Server) openWidgetGrant(token string) (widgetCredential, bool) {
	var c widgetCredential
	if !strings.HasPrefix(token, "pw1.") || len(token) > 16384 {
		return c, false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "pw1."))
	aead := s.widgetAEAD()
	if err != nil || len(b) < aead.NonceSize()+aead.Overhead() {
		return c, false
	}
	data, err := aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], []byte(widgetAAD))
	if err != nil || json.Unmarshal(data, &c) != nil || c.Expires <= time.Now().Unix() {
		return c, false
	}
	u, err := url.Parse(c.Origin)
	return c, err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") &&
		(c.Kind == "cookie" && c.Value != "" || c.Kind == "bearer" && c.Value != "" || c.Kind == "anonymous" && !s.cfg.MultiUser && s.cfg.AuthToken == "")
}

func (s *Server) handleWidgetGrant(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST required")
		return
	}
	var body struct {
		Session string `json:"session"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
		writeErr(w, 400, "session required")
		return
	}
	if _, ok := s.sessionFor(r, body.Session); !ok {
		writeErr(w, 403, "not your conversation")
		return
	}
	token, err := s.mintWidgetGrant(r)
	if err != nil {
		writeErr(w, 401, "sign in again to open the widget")
		return
	}
	writeJSON(w, map[string]any{"token": token, "expiresIn": int(widgetGrantTTL.Seconds())})
}

func widgetAPIAllowed(r *http.Request) bool {
	p := r.URL.Path
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	if strings.HasPrefix(p, "/proxy/") || strings.HasPrefix(p, "/socket.io/") {
		return true
	}
	if strings.HasPrefix(p, "/api/builtin/") || strings.HasPrefix(p, "/api/tool/") {
		return r.Method == http.MethodPost
	}
	for _, prefix := range []string{"/data/", "/plugins/", "/screenshots/", "/fonts/", "/vendor/", "/lib/"} {
		if strings.HasPrefix(p, prefix) {
			return read
		}
	}
	if p == "/api/data-events" || p == "/api/notes" || p == "/api/notes/restore" || p == "/api/notes/share" || p == "/api/notes/image" || p == "/api/tasks" || p == "/api/events" || p == "/api/notify" || p == "/api/chat" || p == "/api/file" || p == "/api/exec" || p == "/api/email/send" || p == "/api/email/drafts" || p == "/api/ai/assist" || p == "/api/email/markseen" || p == "/api/email/tags" {
		return true // Existing endpoint permissions and method checks still apply.
	}
	if read {
		switch p {
		case "/api/tools", "/api/files", "/api/notes/history", "/api/notes/trash", "/api/notes/source", "/api/pim/sources", "/api/email/list", "/api/email/read", "/api/email/folders", "/api/email/search", "/api/email/unread", "/api/email/attachment", "/api/room/history", "/api/room/activity", "/api/activity/work", "/api/vox/history", "/api/chat/attachment", "/api/profile", "/api/agent/name", "/api/models":
			return true
		}
		return p == "/widget-base.css" || p == "/prism-widget.js" || p == "/theme.js"
	}
	return false
}

func (s *Server) setWidgetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: widgetCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode,
		MaxAge: int(widgetGrantTTL.Seconds())})
}

// WidgetHandler is deliberately not a second dashboard. Main cookies and
// deployment bearer tokens alone confer no authority on this listener.
func (s *Server) WidgetHandler() (http.Handler, error) {
	mux, err := s.productRoutes()
	if err != nil {
		return nil, err
	}
	assets, err := fs.Sub(s.cfg.WebFS, "web")
	if err != nil {
		return nil, err
	}
	static := http.FileServer(http.FS(assets))
	authed := s.withAuth(mux)
	return s.lifecycleHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method == http.MethodGet && (r.URL.Path == "/widget-frame.html" || r.URL.Path == "/widget-frame.js") {
			w.Header().Set("Cache-Control", "no-store")
			static.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/__prism_widget/authorize" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != requestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeErr(w, 403, "cross-origin widget request refused")
				return
			}
			if r.Method != http.MethodPost {
				writeErr(w, 405, "POST required")
				return
			}
			var body struct {
				Token string `json:"token"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&body) != nil {
				writeErr(w, 400, "widget token required")
				return
			}
			if _, ok := s.openWidgetGrant(body.Token); !ok {
				writeErr(w, 401, "widget access expired")
				return
			}
			s.setWidgetCookie(w, r, body.Token)
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
		if !widgetAPIAllowed(r) {
			writeErr(w, 403, "not available from a widget")
			return
		}
		token := r.URL.Query().Get("widget_grant")
		if token == "" {
			token = cookieValue(r, widgetCookie)
		}
		credential, ok := s.openWidgetGrant(token)
		if !ok {
			writeErr(w, 401, "widget access expired; reload the widget")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != requestOrigin(r) {
			writeErr(w, 403, "cross-origin widget request refused")
			return
		}
		if r.URL.Query().Has("widget_grant") {
			s.setWidgetCookie(w, r, token)
			if r.Method == http.MethodGet && !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				u := *r.URL
				q := u.Query()
				q.Del("widget_grant")
				u.RawQuery = q.Encode()
				http.Redirect(w, r, u.String(), http.StatusSeeOther)
				return
			}
		}
		clone := r.Clone(r.Context())
		cleanURL := *r.URL
		cleanQuery := cleanURL.Query()
		cleanQuery.Del("widget_grant")
		cleanURL.RawQuery = cleanQuery.Encode()
		clone.URL = &cleanURL
		clone.Header = r.Header.Clone()
		// Preserve service-specific cookies, but never accept the actual Prism
		// dashboard login or another widget credential as implicit authority.
		stripPrismCredentials(clone.Header)
		cookies := clone.Cookies()
		clone.Header.Del("Cookie")
		for _, c := range cookies {
			if c.Name != widgetCookie {
				clone.AddCookie(c)
			}
		}
		if credential.Kind == "cookie" {
			clone.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential.Value})
		} else if credential.Kind == "bearer" {
			clone.Header.Set("Authorization", "Bearer "+credential.Value)
		}
		authed.ServeHTTP(w, clone)
	})), nil
}

func (s *Server) redirectWidgetContent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.WidgetListenAddr == "" || s.cfg.WidgetFrameURL != "" {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin == s.widgetOrigin(r) {
			writeErr(w, 403, "use the widget connection")
			return
		}
		p := r.URL.Path
		active := strings.HasPrefix(p, "/proxy/") || strings.HasPrefix(p, "/socket.io/")
		if strings.HasPrefix(p, "/plugins/") || strings.HasPrefix(p, "/data/") || strings.HasPrefix(p, "/screenshots/") {
			kind := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
			active = active || strings.Contains(kind, "html") || strings.Contains(kind, "svg") || strings.Contains(kind, "xml")
			if kind == "" { // FileServer sniffs unknown extensions as well.
				active = true
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
		if !active {
			next.ServeHTTP(w, r)
			return
		}
		token, err := s.mintWidgetGrant(r)
		if err != nil {
			writeErr(w, 401, "sign in to open this content")
			return
		}
		u, err := url.Parse(s.widgetOrigin(r))
		if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" || u.Scheme+"://"+u.Host == requestOrigin(r) {
			writeErr(w, 503, "a separate widget origin is required")
			return
		}
		u.Path, u.RawPath = r.URL.Path, r.URL.RawPath
		q := r.URL.Query()
		q.Set("widget_grant", token)
		u.RawQuery = q.Encode()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, u.String(), http.StatusTemporaryRedirect)
	})
}

// Preserve the standard port spelling in public configuration diagnostics.
func validWidgetPort(value string) bool {
	p, err := strconv.Atoi(value)
	return err == nil && p > 0 && p <= 65535
}

func (s *Server) validateWidgetConfig() error {
	if s.cfg.WidgetPublicPort != "" && !validWidgetPort(s.cfg.WidgetPublicPort) {
		return fmt.Errorf("WIDGET_PORT must be a valid TCP port")
	}
	if s.cfg.WidgetOrigin != "" {
		u, err := url.Parse(s.cfg.WidgetOrigin)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("WIDGET_ORIGIN must be an HTTP(S) origin, without credentials, a path, or a query")
		}
	}
	return nil
}
