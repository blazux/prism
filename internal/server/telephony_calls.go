package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"prism/internal/memory"
)

// Manual outbound operations belong to the signed-in user. The selected archive
// group and browser-provided scope fields never change ownership or permissions.
func (s *Server) handleTelephonyCalls(w http.ResponseWriter, r *http.Request, u *memory.User, part string) {
	if u.ID < 0 {
		writeErr(w, http.StatusForbidden, "a personal account is required")
		return
	}
	parts := strings.Split(part, "/")
	valid := len(parts) == 1 && (r.Method == http.MethodGet || r.Method == http.MethodPost)
	if len(parts) == 2 || (len(parts) == 3 && parts[2] == "retry") {
		id, err := strconv.ParseInt(parts[1], 10, 64)
		valid = err == nil && id > 0 && ((len(parts) == 2 && r.Method == http.MethodDelete) || (len(parts) == 3 && r.Method == http.MethodPost))
	}
	if !valid {
		writeErr(w, http.StatusMethodNotAllowed, "operation unavailable")
		return
	}
	scope, err := s.voxReadScope(r.Context(), u, false)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	scope["standard"] = false
	q := url.Values{}
	q.Set("limit", r.URL.Query().Get("limit"))
	q.Set("offset", r.URL.Query().Get("offset"))
	if q.Get("limit") == "" {
		q.Set("limit", "25")
	}
	if q.Get("offset") == "" {
		q.Set("offset", "0")
	}
	var body *bytes.Reader
	if len(parts) == 1 && r.Method == http.MethodPost {
		var in struct {
			PhoneNumber string     `json:"phone_number"`
			ContactName string     `json:"contact_name"`
			Mission     string     `json:"mission"`
			ScheduledAt *time.Time `json:"scheduled_at"`
			MaxAttempts int        `json:"max_attempts"`
		}
		in.MaxAttempts = 3
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || strings.TrimSpace(in.PhoneNumber) == "" || strings.TrimSpace(in.Mission) == "" || len(in.Mission) > 16000 || in.MaxAttempts < 1 || in.MaxAttempts > 10 {
			writeErr(w, http.StatusBadRequest, "phone number, mission and 1–10 attempts are required")
			return
		}
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	if body != nil {
		s.forwardVoxScoped(w, r, r.Method, part, q, body, scope)
	} else {
		s.forwardVoxScoped(w, r, r.Method, part, q, nil, scope)
	}
}
