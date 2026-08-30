package server

import (
	"net/http"
	"testing"
)

func TestProxyServiceName(t *testing.T) {
	for _, ok := range []string{"grafana", "uptime-kuma", "svc_2", "a"} {
		if !proxyServiceNameRe.MatchString(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "x.attacker.com", "-lead", "UPPER", "a b", "host:80", "../x"} {
		if proxyServiceNameRe.MatchString(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestResolveProxyTargetRejectsHostnames(t *testing.T) {
	s := &Server{cfg: Config{AgentContainer: "prism-workspace"}}
	if host, ok := s.resolveProxyTarget("http://prism/proxy/x.attacker.com/80/"); ok {
		t.Fatalf("hostname accepted as service name: %s", host)
	}
	if host, ok := s.resolveProxyTarget("http://prism/proxy/grafana/3000/"); !ok || host != "prism-svc-grafana:3000" {
		t.Fatalf("legit route broken: %v %s", ok, host)
	}
}

func TestStripPrismCredentials(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer deployment-token")
	h.Add("Cookie", sessionCookie+"=abc; grafana_session=keep")
	h.Add("Cookie", "other=1")
	stripPrismCredentials(h)
	if h.Get("Authorization") != "" {
		t.Error("bearer token forwarded to proxied service")
	}
	got := h.Get("Cookie")
	if got != "grafana_session=keep; other=1" {
		t.Errorf("cookie header = %q", got)
	}
	// Only Prism's cookie: header disappears entirely.
	h2 := http.Header{"Cookie": {sessionCookie + "=abc"}}
	stripPrismCredentials(h2)
	if _, present := h2["Cookie"]; present {
		t.Error("empty Cookie header left behind")
	}
}
