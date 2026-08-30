package server

import (
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// handleSocketIOProxy catches /socket.io/ requests originating from pages
// served via /proxy/ and routes them to the correct backend service.
//
// Routing strategy (in order):
//  1. sid in URL params → look up cached sid→host mapping (covers WebSocket
//     upgrades where Referer is no longer the proxy URL after pushState).
//  2. Referer / Origin header → resolve service, proxy request, AND capture
//     the sid from the handshake response to seed the cache.
func (s *Server) handleSocketIOProxy(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("sid")

	// Fast path: sid already mapped.
	if sid != "" {
		if host, ok := s.socketSessions.Load(sid); ok {
			s.reverseProxy(w, r, host.(string), r.URL.Path, "")
			return
		}
	}

	// Resolve via Referer (HTTP polling) or Origin (WebSocket upgrade).
	ref := r.Header.Get("Referer")
	if ref == "" {
		ref = r.Header.Get("Origin")
	}
	targetHost, ok := s.resolveProxyTarget(ref)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Store existing sid if present (e.g. WebSocket with a known-but-uncached sid).
	if sid != "" {
		s.socketSessions.Store(sid, targetHost)
		s.reverseProxy(w, r, targetHost, r.URL.Path, "")
		return
	}

	// Initial handshake (no sid yet): proxy and capture the sid from the response.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.tunnelWebSocket(w, r, targetHost, r.URL.Path)
		return
	}
	targetURL := &url.URL{Scheme: "http", Host: targetHost}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	host := targetHost
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = host
		req.URL.Path = r.URL.Path
		req.Host = host
		stripPrismCredentials(req.Header)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if newSid := extractSocketIOSid(string(body)); newSid != "" {
			s.socketSessions.Store(newSid, host)
		}
		resp.Body = io.NopCloser(strings.NewReader(string(body)))
		return nil
	}
	proxy.FlushInterval = -1
	proxy.ServeHTTP(w, r)
}

// extractSocketIOSid parses the sid from a socket.io handshake response body.
// Format: <length>:0{"sid":"<sid>","upgrades":[...],...}
func extractSocketIOSid(body string) string {
	idx := strings.Index(body, `"sid":"`)
	if idx == -1 {
		return ""
	}
	rest := body[idx+7:]
	end := strings.IndexByte(rest, '"')
	if end == -1 {
		return ""
	}
	return rest[:end]
}

// resolveProxyTarget extracts the Docker service host from a /proxy/ URL
// (found in Referer or Origin headers).
func (s *Server) resolveProxyTarget(referer string) (string, bool) {
	if referer == "" {
		return "", false
	}
	u, err := url.Parse(referer)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimPrefix(u.Path, "/proxy/")
	if trimmed == u.Path {
		return "", false
	}
	parts := strings.SplitN(trimmed, "/", 3)
	// Legacy route: /proxy/<port>/
	if port, err := strconv.Atoi(parts[0]); err == nil && port >= 1 && port <= 65535 {
		return fmt.Sprintf("%s:%d", s.cfg.AgentContainer, port), true
	}
	// Named route: /proxy/<name>/<port>/
	if len(parts) < 2 {
		return "", false
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil || port < 1 || port > 65535 || !proxyServiceNameRe.MatchString(parts[0]) {
		return "", false
	}
	return fmt.Sprintf("prism-svc-%s:%d", parts[0], port), true
}

// proxyServiceNameRe bounds the <name> segment of /proxy/<name>/<port>/ to a
// container slug. Unbounded, "prism-svc-" + name became an SSRF primitive: a
// name like "x.attacker.com" resolves wherever the attacker's DNS says, and
// the request carried the victim's Prism session cookie.
var proxyServiceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// stripPrismCredentials removes Prism's own session cookie and bearer token
// from a request about to leave for a proxied service. Other cookies (the
// service's own login, e.g. Grafana's) pass through untouched.
func stripPrismCredentials(h http.Header) {
	if auth := h.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		h.Del("Authorization")
	}
	raw := h.Values("Cookie")
	if len(raw) == 0 {
		return
	}
	h.Del("Cookie")
	var kept []string
	for _, line := range raw {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" || strings.HasPrefix(part, sessionCookie+"=") {
				continue
			}
			kept = append(kept, part)
		}
	}
	if len(kept) > 0 {
		h.Set("Cookie", strings.Join(kept, "; "))
	}
}

// handleWorkspaceProxy reverse-proxies requests to services running in Docker.
//
// Routes:
//
//	/proxy/<port>/...          → workspace container at <port>  (legacy)
//	/proxy/<name>/<port>/...   → prism-svc-<name> container at <port>
func (s *Server) handleWorkspaceProxy(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/proxy/")
	firstSlash := strings.Index(trimmed, "/")
	var firstSeg, rest string
	if firstSlash < 0 {
		firstSeg = trimmed
		rest = "/"
	} else {
		firstSeg = trimmed[:firstSlash]
		rest = trimmed[firstSlash:]
	}

	port, err := strconv.Atoi(firstSeg)
	if err == nil && port >= 1 && port <= 65535 {
		// Legacy: route to workspace container.
		s.reverseProxy(w, r, fmt.Sprintf("%s:%d", s.cfg.AgentContainer, port), rest, "/proxy/"+firstSeg)
		return
	}

	// Service container route: /proxy/<name>/<port>/...
	restTrimmed := strings.TrimPrefix(rest, "/")
	secondSlash := strings.Index(restTrimmed, "/")
	var portStr, subPath string
	if secondSlash < 0 {
		portStr = restTrimmed
		subPath = "/"
	} else {
		portStr = restTrimmed[:secondSlash]
		subPath = restTrimmed[secondSlash:]
	}
	svcPort, err := strconv.Atoi(portStr)
	if err != nil || svcPort < 1 || svcPort > 65535 {
		http.Error(w, "invalid proxy route", http.StatusBadRequest)
		return
	}
	if !proxyServiceNameRe.MatchString(firstSeg) {
		http.Error(w, "invalid proxy route", http.StatusBadRequest)
		return
	}
	prefix := "/proxy/" + firstSeg + "/" + portStr
	s.reverseProxy(w, r, fmt.Sprintf("prism-svc-%s:%d", firstSeg, svcPort), subPath, prefix)
}

func (s *Server) reverseProxy(w http.ResponseWriter, r *http.Request, targetHost, subPath, prefix string) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.tunnelWebSocket(w, r, targetHost, subPath)
		return
	}
	targetURL := &url.URL{Scheme: "http", Host: targetHost}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = targetHost
		req.URL.Path = subPath
		req.Host = targetHost
		stripPrismCredentials(req.Header)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Strip framing restrictions so the service can be embedded as an iframe
		// in the Prism dashboard (same origin via proxy bypasses X-Frame-Options).
		resp.Header.Del("X-Frame-Options")
		if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
			// Remove frame-ancestors directive so the browser allows framing.
			parts := strings.Split(csp, ";")
			var kept []string
			for _, p := range parts {
				if !strings.Contains(strings.ToLower(strings.TrimSpace(p)), "frame-ancestors") {
					kept = append(kept, p)
				}
			}
			if len(kept) > 0 {
				resp.Header.Set("Content-Security-Policy", strings.Join(kept, ";"))
			} else {
				resp.Header.Del("Content-Security-Policy")
			}
		}
		// Rewrite Location headers for redirects.
		if loc := resp.Header.Get("Location"); loc != "" {
			if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, prefix) {
				resp.Header.Set("Location", prefix+loc)
			}
		}
		// Rewrite absolute asset paths in HTML and JS responses (Vite/SPA builds use
		// paths like /assets/... which the browser resolves against the origin, not the
		// proxy prefix).
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "javascript") {
			return nil
		}
		var reader io.Reader = resp.Body
		if resp.Header.Get("Content-Encoding") == "gzip" {
			gr, err := gzip.NewReader(resp.Body)
			if err != nil {
				return err
			}
			defer gr.Close()
			reader = gr
		}
		body, err := io.ReadAll(reader)
		resp.Body.Close()
		if err != nil {
			resp.Body = io.NopCloser(strings.NewReader(""))
			return err
		}
		rewritten := rewriteAbsolutePaths(string(body), prefix)
		// For HTML pages, inject a script that wraps history.pushState /
		// replaceState / location.assign so SPA routers (Vue, React, etc.) stay
		// within the proxy prefix even when they navigate to absolute paths.
		if strings.Contains(ct, "text/html") && prefix != "" {
			rewritten = injectHistoryFix(rewritten, prefix)
		}
		resp.Body = io.NopCloser(strings.NewReader(rewritten))
		resp.ContentLength = int64(len(rewritten))
		resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
		resp.Header.Del("Content-Encoding") // body is now decompressed plain text
		return nil
	}
	proxy.FlushInterval = -1
	proxy.ServeHTTP(w, r)
}

// injectHistoryFix injects a script into HTML that wraps history.pushState,
// history.replaceState and location.assign/replace so SPA routers (Vue, React,
// Angular…) stay within the proxy prefix when navigating to absolute paths.
func injectHistoryFix(body, prefix string) string {
	script := fmt.Sprintf(`<script>(function(){`+
		`var b=%q;`+
		`function f(u){return u&&typeof u==='string'&&u.charAt(0)==='/'&&u.indexOf(b)!==0?b+u:u;}`+
		`var oP=history.pushState,oR=history.replaceState;`+
		`history.pushState=function(s,t,u){return oP.call(this,s,t,f(u));};`+
		`history.replaceState=function(s,t,u){return oR.call(this,s,t,f(u));};`+
		`var oA=location.assign.bind(location),oRp=location.replace.bind(location);`+
		`location.assign=function(u){return oA(f(u));};`+
		`location.replace=function(u){return oRp(f(u));};`+
		`})();</script>`, prefix)
	// Inject as the very first script in <head> so it runs before any framework.
	if idx := strings.Index(body, "<head>"); idx != -1 {
		return body[:idx+6] + script + body[idx+6:]
	}
	// Fallback: prepend to body.
	return script + body
}

// rewriteAbsolutePaths replaces absolute paths in HTML/JS bodies with the
// proxy prefix so browsers resolve assets correctly. Idempotent: paths already
// starting with prefix are left untouched to avoid double-prefixing.
func rewriteAbsolutePaths(body, prefix string) string {
	if prefix == "" {
		return body
	}
	prefix = strings.TrimRight(prefix, "/")
	for _, attr := range []string{
		`src="`, `href="`, `action="`, `content="`,
		`src='`, `href='`, `action='`,
		`url("`, `url('`,
		`from "`, `import("`,
	} {
		body = rewriteAttr(body, attr, prefix)
	}
	return body
}

// rewriteAttr rewrites attr+"/" → attr+prefix+"/" while skipping occurrences
// that are already prefixed (prevents double-prefixing when the app itself
// sets a base path like URL_BASE_PATH).
func rewriteAttr(body, attr, prefix string) string {
	search := attr + "/"
	already := attr + prefix + "/"
	var sb strings.Builder
	for {
		idx := strings.Index(body, search)
		if idx == -1 {
			sb.WriteString(body)
			return sb.String()
		}
		sb.WriteString(body[:idx])
		rest := body[idx:]
		if strings.HasPrefix(rest, already) {
			// Already prefixed — copy as-is and skip past it.
			sb.WriteString(already)
			body = rest[len(already):]
		} else {
			// Bare absolute path — inject prefix.
			sb.WriteString(already)
			body = rest[len(search):]
		}
	}
}

func (s *Server) tunnelWebSocket(w http.ResponseWriter, r *http.Request, targetHost, subPath string) {
	dst, err := net.DialTimeout("tcp", targetHost, 10*time.Second)
	if err != nil {
		http.Error(w, "workspace service unreachable", http.StatusBadGateway)
		return
	}
	defer dst.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	src, _, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer src.Close()

	// Re-send the original upgrade request to the target.
	fwd := r.Clone(r.Context())
	fwd.URL = &url.URL{Path: subPath, RawQuery: r.URL.RawQuery}
	fwd.RequestURI = ""
	stripPrismCredentials(fwd.Header)
	if err := fwd.Write(dst); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { io.Copy(dst, src); done <- struct{}{} }()
	go func() { io.Copy(src, dst); done <- struct{}{} }()
	<-done
}

// ─── Auth ─────────────────────────────────────────────────────────────────────
