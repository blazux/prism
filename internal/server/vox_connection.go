package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"prism/internal/agent"
)

const voxConnectionSecret = "prism_telephony_connection"

// One encrypted installation record; no board, user, or group owns the link.
// Published snapshots are immutable so active requests retain coherent credentials.
type voxConnection struct {
	Enabled        bool     `json:"enabled"`
	URL            string   `json:"url"`
	User           string   `json:"user"`
	Password       string   `json:"password,omitempty"`
	PrismURL       string   `json:"prism_url"`
	PrismPublicURL string   `json:"prism_public_url"`
	Model          string   `json:"model"`
	GatewayToken   string   `json:"gateway_token,omitempty"`
	PreviousModels []string `json:"previous_models,omitempty"`
}

var voxControlHTTP = &http.Client{
	Timeout:       6 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (s *Server) voxConnection() voxConnection {
	if c := s.voxState.Load(); c != nil {
		return *c
	}
	return voxConnection{Enabled: s.cfg.VoxURL != "", URL: s.cfg.VoxURL, User: s.cfg.VoxUser, Password: s.cfg.VoxPassword}
}

func (s *Server) voxURL() string {
	c := s.voxConnection()
	if !c.Enabled {
		return ""
	}
	return c.URL
}

func (s *Server) voxCredentials() (string, string, string) {
	c := s.voxConnection()
	if !c.Enabled {
		return "", "", ""
	}
	return c.URL, c.User, c.Password
}

func (s *Server) configureVoxExecutor(e *agent.ToolExecutor, session string) {
	e.SetVoxProvider(s.voxCredentials)
	e.SetVoxScopeProvider(func() string { return s.voxScopeHeader(context.Background(), session) })
	e.SetCallQueuedCallback(func(ctx context.Context, call agent.QueuedCall) error { return s.trackVoxCall(ctx, session, call) })
}

func (s *Server) loadVoxConnection(ctx context.Context) {
	ms := s.store()
	if ms == nil {
		return
	}
	raw, found, err := ms.GetSecret(ctx, voxConnectionSecret)
	if err != nil {
		s.voxState.Store(&voxConnection{})
		log.Printf("[telephony] cannot load saved connection")
		return
	}
	if !found {
		return
	}
	var c voxConnection
	if json.Unmarshal([]byte(raw), &c) != nil {
		s.voxState.Store(&voxConnection{})
		log.Printf("[telephony] invalid saved connection")
		return
	}
	s.voxState.Store(&c)
}

func (s *Server) saveVoxConnection(ctx context.Context, c voxConnection) error {
	ms := s.store()
	if ms == nil {
		return errors.New("configuration database unavailable")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := ms.SetSecret(ctx, voxConnectionSecret, string(raw)); err != nil {
		return errors.New("could not save the connection")
	}
	s.voxState.Store(&c)
	return nil
}

func normalizeVoxURL(raw string, optional bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if optional && raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("use an HTTP(S) address without embedded credentials, query or fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func canManageTelephony(s *Server, r *http.Request) bool {
	u := currentUser(r)
	return u != nil && (!s.cfg.MultiUser || u.IsGlobalAdmin())
}

func (s *Server) readVoxConnectionDraft(r *http.Request) (voxConnection, error) {
	old := s.voxConnection()
	var in struct {
		URL            *string `json:"url"`
		User           *string `json:"user"`
		Password       string  `json:"password"`
		PrismURL       *string `json:"prism_url"`
		PrismPublicURL *string `json:"prism_public_url"`
		Model          *string `json:"model"`
		Enabled        *bool   `json:"enabled"`
		ClearPassword  bool    `json:"clear_password"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in) != nil {
		return old, errors.New("invalid connection settings")
	}
	c := old
	var err error
	if in.URL != nil {
		if c.URL, err = normalizeVoxURL(*in.URL, false); err != nil {
			return c, err
		}
	}
	if c.URL == "" {
		return c, errors.New("enter the PrismVox address")
	}
	if in.User != nil {
		c.User = strings.TrimSpace(*in.User)
	}
	if in.Password != "" || in.ClearPassword {
		c.Password = in.Password
	} else if c.URL != old.URL || c.User != old.User {
		// Never send a previous endpoint's secret to a different destination.
		c.Password = ""
	}
	if in.PrismURL != nil {
		if c.PrismURL, err = normalizeVoxURL(*in.PrismURL, true); err != nil {
			return c, fmt.Errorf("Prism address: %w", err)
		}
	}
	if in.PrismPublicURL != nil {
		if c.PrismPublicURL, err = normalizeVoxURL(*in.PrismPublicURL, true); err != nil {
			return c, fmt.Errorf("public Prism address: %w", err)
		}
	}
	if in.Model != nil {
		c.Model = strings.TrimSpace(*in.Model)
	}
	if len(c.Model) > 250 || strings.ContainsAny(c.Model, "\r\n") {
		return c, errors.New("invalid model selector")
	}
	c.Enabled = true
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	return c, nil
}

func callVoxControl(ctx context.Context, c voxConnection, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return 0, errors.New("invalid phone-service address")
	}
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Password)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := voxControlHTTP.Do(req)
	if err != nil {
		return 0, errors.New("phone service is unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("phone service returned HTTP %d", resp.StatusCode)
	}
	if out != nil && json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out) != nil {
		return resp.StatusCode, errors.New("invalid phone-service response")
	}
	return resp.StatusCode, nil
}

func (s *Server) publicVoxConnection(ctx context.Context, c voxConnection) map[string]any {
	out := map[string]any{"enabled": c.Enabled, "url": c.URL, "user": c.User, "prism_url": c.PrismURL, "prism_public_url": c.PrismPublicURL, "model": c.Model, "password_configured": c.Password != "", "configured": c.URL != "" && c.Enabled, "reachable": false, "authenticated": false, "reverse_connected": false}
	if c.URL == "" {
		return out
	}
	var settings map[string]any
	status, err := callVoxControl(ctx, c, http.MethodGet, "/api/prism/connection", nil, &settings)
	out["reachable"] = status != 0
	if err != nil {
		if status == http.StatusNotFound {
			var legacy map[string]any
			if _, probeErr := callVoxControl(ctx, c, http.MethodGet, "/api/config", nil, &legacy); probeErr == nil {
				out["authenticated"] = true
				out["pairing_supported"] = false
				out["detail"] = "Update PrismVox to manage the two-way connection and Prism models from this screen."
				return out
			}
		}
		if status == 401 || status == 403 {
			out["detail"] = "Phone-service credentials were refused."
		} else if status == 404 {
			out["detail"] = "Update PrismVox to configure its connection from Prism."
		} else {
			out["detail"] = err.Error()
		}
		return out
	}
	out["authenticated"] = true
	out["pairing_supported"] = true
	out["model_synchronized"] = settings["model"] == c.Model
	if settings["model"] != c.Model {
		out["detail"] = "The phone model differs from the saved Prism selection. Save the phone model again to synchronize it."
	}
	var reverse map[string]any
	if _, err := callVoxControl(ctx, c, http.MethodGet, "/api/prism/connection/status", nil, &reverse); err == nil {
		out["reverse_connected"] = reverse["connected"] == true && settings["prism_url"] == c.PrismURL && settings["enabled"] == true
		if detail, ok := reverse["detail"].(string); ok {
			out["reverse_detail"] = detail
		}
	}
	return out
}

func (s *Server) handleVoxConnectionTest(w http.ResponseWriter, r *http.Request) {
	if !canManageTelephony(s, r) {
		writeErr(w, 403, "owner or global administrator only")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST required")
		return
	}
	c, err := s.readVoxConnectionDraft(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.publicVoxConnection(r.Context(), c))
}

func (s *Server) handleVoxConnection(w http.ResponseWriter, r *http.Request) {
	if !canManageTelephony(s, r) {
		writeErr(w, 403, "owner or global administrator only")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		writeJSON(w, s.publicVoxConnection(r.Context(), s.voxConnection()))
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeErr(w, 405, "GET, PUT or DELETE required")
		return
	}
	s.voxConfigMu.Lock()
	defer s.voxConfigMu.Unlock()
	old := s.voxConnection()
	c := old
	if r.Method == http.MethodDelete {
		c.Enabled = false
	} else {
		var err error
		c, err = s.readVoxConnectionDraft(r)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if s.store() == nil {
		writeErr(w, 503, "configuration database unavailable")
		return
	}
	newGateway := c.GatewayToken == "" || (c.Enabled && (!old.Enabled || c.URL != old.URL || c.User != old.User || c.PrismURL != old.PrismURL))
	if newGateway {
		token, err := newSessionToken()
		if err != nil {
			writeErr(w, 500, "could not create the phone connection")
			return
		}
		c.GatewayToken = token
		c.PreviousModels = nil
	} else if c.Model != old.Model && old.Model != "" {
		// Existing calls retain their model snapshot. Only models previously
		// selected by the owner for this same trusted gateway are accepted.
		previous := append([]string(nil), old.PreviousModels...)
		found := false
		for _, model := range previous {
			if model == old.Model {
				found = true
				break
			}
		}
		if !found {
			previous = append(previous, old.Model)
		}
		if len(previous) > 20 {
			previous = previous[len(previous)-20:]
		}
		c.PreviousModels = previous
	}
	if c.Enabled && c.PrismURL == "" {
		writeErr(w, 400, "enter the Prism address reachable from PrismVox")
		return
	}
	if c.Enabled {
		status := s.publicVoxConnection(r.Context(), c)
		if status["authenticated"] != true {
			writeErr(w, 502, fmt.Sprint(status["detail"]))
			return
		}
	}
	// Save first so the callback can authenticate immediately. Roll back the
	// installation record if Vox refuses its half of the connection.
	if err := s.saveVoxConnection(r.Context(), c); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	remote := map[string]any{"enabled": c.Enabled, "prism_url": c.PrismURL, "prism_public_url": c.PrismPublicURL, "model": c.Model}
	if c.Enabled {
		remote["prism_token"] = c.GatewayToken
	}
	if _, err := callVoxControl(r.Context(), c, http.MethodPut, "/api/prism/connection", remote, nil); err != nil {
		if !c.Enabled {
			s.broadcastTools()
			status := s.publicVoxConnection(r.Context(), c)
			status["detail"] = "Disconnected in Prism. PrismVox could not be reached to update its connection."
			writeJSON(w, status)
			return
		}
		if rollbackErr := s.saveVoxConnection(r.Context(), old); rollbackErr != nil {
			log.Printf("[telephony] connection rollback failed")
		}
		writeErr(w, 502, "PrismVox did not accept the connection; previous settings were retained")
		return
	}
	s.broadcastTools()
	writeJSON(w, s.publicVoxConnection(r.Context(), c))
}

// The pairing credential authenticates only the phone gateway, never arbitrary
// administration, files, widgets, user API calls or a browser chat.
func (s *Server) voxGatewayAuthed(r *http.Request) bool {
	token := bearerToken(r)
	c := s.voxConnection()
	if !c.Enabled || c.GatewayToken == "" || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(c.GatewayToken)) != 1 {
		return false
	}
	return voxGatewayPath(r)
}

func voxGatewayPath(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/voice/caller", "/api/voice/directory", "/api/voice/search", "/api/voice/capabilities", "/api/voice/inference/v1/models", "/api/voice/inference/v1/chat/completions":
		return true
	case "/ws":
		return r.URL.Query().Get("channel") == voiceChannelName
	}
	return false
}

func (s *Server) voxConnectionBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Older gateways can still hold the deployment token. An explicit UI
		// disconnect must stop their phone requests too, without revoking the
		// token used by unrelated integrations or breaking bootstrap installs.
		if c := s.voxState.Load(); c != nil && !c.Enabled && voxGatewayPath(r) {
			writeErr(w, http.StatusServiceUnavailable, "The PrismVox connection is disconnected.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleVoiceCapabilities(w http.ResponseWriter, r *http.Request) {
	if !canManageTelephony(s, r) {
		writeErr(w, 403, "phone gateway only")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET required")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"ok": true, "service": "prism", "voice": true, "inference": true, "models": true, "instance": s.voxInstanceID(r.Context())})
}
