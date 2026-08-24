package server

import (
	"os"
	"strconv"
	"sync"
)

// toolLimiter bounds how many tool executions can be in flight at once, so a
// single misbehaving widget — e.g. a button stuck in a fetch() loop — can't
// spawn unbounded `docker exec` processes and OOM the host. Each /api/tool and
// /api/builtin call reserves a slot; over either cap the caller gets 429
// immediately and NO exec is spawned. It caps two ways:
//
//   - per session: stops one runaway dashboard from saturating everything;
//   - global: protects the host no matter how many sessions misbehave.
//
// Both are env-tunable (MAX_TOOL_CONCURRENCY, MAX_TOOL_CONCURRENCY_PER_SESSION).
// A refusal is deliberately non-blocking: queueing a flood would just defer the
// same memory pressure instead of shedding it.
type toolLimiter struct {
	global      chan struct{}
	mu          sync.Mutex
	perSession  map[string]int
	perSessionN int
}

func newToolLimiter() *toolLimiter {
	global := toolEnvInt("MAX_TOOL_CONCURRENCY", 24)
	per := toolEnvInt("MAX_TOOL_CONCURRENCY_PER_SESSION", 6)
	if global < 1 {
		global = 1
	}
	if per < 1 {
		per = 1
	}
	return &toolLimiter{
		global:      make(chan struct{}, global),
		perSession:  map[string]int{},
		perSessionN: per,
	}
}

// acquire reserves a slot for sessionID. It returns a release func and true on
// success, or nil/false if the per-session or global cap is already reached.
// release is idempotent and safe to defer.
func (l *toolLimiter) acquire(sessionID string) (func(), bool) {
	l.mu.Lock()
	if l.perSession[sessionID] >= l.perSessionN {
		l.mu.Unlock()
		return nil, false
	}
	select {
	case l.global <- struct{}{}: // reserve global slot without blocking
	default:
		l.mu.Unlock()
		return nil, false
	}
	l.perSession[sessionID]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.perSession[sessionID] > 0 {
				l.perSession[sessionID]--
				if l.perSession[sessionID] == 0 {
					delete(l.perSession, sessionID)
				}
			}
			l.mu.Unlock()
			<-l.global
		})
	}, true
}

func toolEnvInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
