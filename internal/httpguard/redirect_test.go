package httpguard

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func redirectRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Request{URL: u}
}

func TestSameOriginRedirect(t *testing.T) {
	for _, test := range []struct {
		name, initial, destination string
		allowed                    bool
	}{
		{"relative canonical path", "https://api.example/v1/chat", "https://api.example/canonical", true},
		{"hostname case", "https://API.EXAMPLE/a", "https://api.example/b", true},
		{"default HTTPS port", "https://api.example/a", "https://api.example:443/b", true},
		{"default HTTP port", "http://api.example:80/a", "http://api.example/b", true},
		{"numeric port", "https://api.example:0443/a", "https://api.example:443/b", true},
		{"IPv6 canonical address", "http://[0:0:0:0:0:0:0:1]:8080/a", "http://[::1]:8080/b", true},
		{"query changes", "http://api.example/a?first=yes", "http://api.example/a?next=yes", true},
		{"port change", "http://api.example:8080/a", "http://api.example:8081/b", false},
		{"hostname change", "https://api.example/a", "https://other.example/b", false},
		{"subdomain change", "https://api.example/a", "https://sub.api.example/b", false},
		{"HTTPS downgrade", "https://api.example/a", "http://api.example/b", false},
		{"HTTP upgrade", "http://api.example/a", "https://api.example/b", false},
		{"IPv6 zone change", "http://[fe80::1%25ETH0]/a", "http://[fe80::1%25eth0]/b", false},
		{"non HTTP scheme", "https://api.example/a", "file:///tmp/b", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := SameOriginRedirect(redirectRequest(t, test.destination), []*http.Request{redirectRequest(t, test.initial)})
			if test.allowed && err != nil {
				t.Fatalf("same-origin redirect refused: %v", err)
			}
			if !test.allowed && !errors.Is(err, ErrRedirect) {
				t.Fatal("origin change did not produce a permanent policy refusal")
			}
		})
	}
}

func TestRedirectLimitAndInitialOrigin(t *testing.T) {
	initial := redirectRequest(t, "https://api.example/initial")
	next := redirectRequest(t, "https://api.example/next")
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = initial
	}
	if err := SameOriginRedirect(next, via[:9]); err != nil {
		t.Fatal("redirect limit was reduced below net/http's default")
	}
	if !errors.Is(SameOriginRedirect(next, via), ErrRedirect) {
		t.Fatal("redirect loop was not stopped at net/http's default limit")
	}
	other := redirectRequest(t, "https://other.example/next")
	if !errors.Is(SameOriginRedirect(other, []*http.Request{initial, other}), ErrRedirect) {
		t.Fatal("redirect policy trusted the previous hop instead of the configured origin")
	}
	if !errors.Is(SameOriginRedirect(next, nil), ErrRedirect) {
		t.Fatal("redirect without an initial origin was accepted")
	}
}
