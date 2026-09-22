package server

import (
	"net"
	"net/http"
	"time"
)

type authWindow struct {
	start time.Time
	count int
}

// Bound authentication work even when no database is available. Do not trust a
// client-supplied forwarding header as an identity for rate limiting.
func (s *Server) allowAuthentication(r *http.Request) bool {
	if r.Method != http.MethodPost || (r.URL.Path != "/api/login" && r.URL.Path != "/api/signup" && r.URL.Path != "/api/auth") {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authWindows == nil {
		s.authWindows = map[string]authWindow{}
	}
	for key, w := range s.authWindows {
		if now.Sub(w.start) >= time.Minute {
			delete(s.authWindows, key)
		}
	}
	key := host + ":" + r.URL.Path
	w := s.authWindows[key]
	if w.start.IsZero() {
		if len(s.authWindows) >= 10000 {
			return false
		}
		w.start = now
	}
	limit := 30
	if r.URL.Path == "/api/signup" {
		limit = 10
	}
	if w.count >= limit {
		return false
	}
	w.count++
	s.authWindows[key] = w
	return true
}
