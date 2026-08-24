package server

import "testing"

func newTestLimiter(global, perSession int) *toolLimiter {
	return &toolLimiter{
		global:      make(chan struct{}, global),
		perSession:  map[string]int{},
		perSessionN: perSession,
	}
}

func TestToolLimiterPerSessionCap(t *testing.T) {
	l := newTestLimiter(100, 2)

	r1, ok := l.acquire("s")
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	if _, ok := l.acquire("s"); !ok {
		t.Fatal("second acquire (at cap) should succeed")
	}
	if _, ok := l.acquire("s"); ok {
		t.Fatal("third acquire should be refused — per-session cap is 2")
	}
	// A different session is unaffected by s's saturation.
	if _, ok := l.acquire("other"); !ok {
		t.Fatal("a different session must not be blocked by another's load")
	}
	// Releasing one of s's slots frees capacity again.
	r1()
	if _, ok := l.acquire("s"); !ok {
		t.Fatal("acquire should succeed after a release")
	}
}

func TestToolLimiterGlobalCap(t *testing.T) {
	l := newTestLimiter(3, 2) // global 3, per-session 2

	if _, ok := l.acquire("a"); !ok {
		t.Fatal("a#1")
	}
	if _, ok := l.acquire("a"); !ok {
		t.Fatal("a#2")
	}
	if _, ok := l.acquire("b"); !ok {
		t.Fatal("b#1 (3rd global slot)")
	}
	// Global pool (3) is now exhausted; a fresh session is still refused.
	if _, ok := l.acquire("c"); ok {
		t.Fatal("global cap reached — must refuse even a new session")
	}
}

func TestToolLimiterReleaseIdempotent(t *testing.T) {
	l := newTestLimiter(1, 5)
	r, _ := l.acquire("s")
	r()
	r() // double release must not over-drain the global channel
	if _, ok := l.acquire("s"); !ok {
		t.Fatal("capacity should be intact after a double release")
	}
}
