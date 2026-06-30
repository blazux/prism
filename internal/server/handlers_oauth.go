package server

// OAuth handlers for the "bring your own app" flow (Google, Microsoft…).
// /api/oauth/<provider>/config  — GET status, POST creds / disconnect
// /api/oauth/<provider>/start    — redirect the browser to the consent screen
// /api/oauth/<provider>/callback — exchange the code and store the token

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"prism/internal/oauthx"
)

type oauthState struct {
	provider string
	redirect string
	exp      time.Time
}

// externalBase reconstructs the URL the browser reached Prism at (honouring a
// reverse proxy), so the OAuth redirect URI matches what the user registered.
func externalBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

func randState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) handleOAuth(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/oauth/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "not found", 404)
		return
	}
	provider, action := parts[0], parts[1]
	if _, ok := oauthx.Providers[provider]; !ok {
		http.Error(w, "unknown provider", 404)
		return
	}
	redirect := externalBase(r) + "/api/oauth/" + provider + "/callback"

	switch action {
	case "config":
		s.oauthConfig(w, r, provider, redirect)
	case "start":
		state := randState()
		url, err := oauthx.AuthCodeURL(r.Context(), s.memStore, provider, redirect, state)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.oauthStates.Store(state, oauthState{provider: provider, redirect: redirect, exp: time.Now().Add(10 * time.Minute)})
		http.Redirect(w, r, url, http.StatusFound)
	case "callback":
		s.oauthCallback(w, r, provider)
	default:
		http.Error(w, "not found", 404)
	}
}

func (s *Server) oauthConfig(w http.ResponseWriter, r *http.Request, provider, redirect string) {
	switch r.Method {
	case "GET":
		writeJSON(w, map[string]interface{}{
			"connected":   oauthx.Connected(r.Context(), s.memStore, provider),
			"clientId":    oauthx.ClientID(r.Context(), s.memStore, provider),
			"redirectUri": redirect,
		})
	case "POST":
		var b struct {
			ClientID     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
			Disconnect   bool   `json:"disconnect"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Disconnect {
			oauthx.Disconnect(r.Context(), s.memStore, provider)
			writeJSON(w, map[string]interface{}{"ok": true, "connected": false})
			return
		}
		if strings.TrimSpace(b.ClientID) == "" || strings.TrimSpace(b.ClientSecret) == "" {
			http.Error(w, "client id and secret required", 400)
			return
		}
		if err := oauthx.SaveClient(r.Context(), s.memStore, provider, strings.TrimSpace(b.ClientID), strings.TrimSpace(b.ClientSecret)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request, provider string) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		oauthDonePage(w, "Authorization was denied: "+e)
		return
	}
	state := q.Get("state")
	v, ok := s.oauthStates.LoadAndDelete(state)
	if !ok {
		oauthDonePage(w, "Invalid or expired authorization. Please try again.")
		return
	}
	st := v.(oauthState)
	if st.provider != provider || time.Now().After(st.exp) {
		oauthDonePage(w, "Authorization expired. Please try again.")
		return
	}
	if err := oauthx.Exchange(r.Context(), s.memStore, provider, st.redirect, q.Get("code")); err != nil {
		oauthDonePage(w, "Could not complete sign-in: "+err.Error())
		return
	}
	oauthDonePage(w, "")
}

// oauthDonePage renders a tiny page that notifies the opener and closes itself.
// msg empty ⇒ success.
func oauthDonePage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	status := "Connected ✓ — you can close this window."
	ok := "true"
	if msg != "" {
		status = msg
		ok = "false"
	}
	w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Prism</title>
<style>body{font:14px -apple-system,system-ui,sans-serif;background:#0b0d12;color:#dce0e8;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center;padding:24px}</style>
</head><body><div><p>` + status + `</p></div>
<script>try{window.opener&&window.opener.postMessage({type:'oauth-done',ok:` + ok + `},'*')}catch(e){};setTimeout(function(){window.close()}, ` + boolPick(ok, "800", "4000") + `)</script>
</body></html>`))
}

func boolPick(ok, a, b string) string {
	if ok == "true" {
		return a
	}
	return b
}
