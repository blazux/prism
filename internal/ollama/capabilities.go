package ollama

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// ModelCapabilities describes the effective context, not a name-based guess.
// Zero/nil means the provider did not supply the information.
type ModelCapabilities struct {
	ContextWindow int   `json:"contextWindow,omitempty"`
	Vision        *bool `json:"vision,omitempty"`
}

// Optional: existing backends and integrations keep the small Backend contract.
type CapabilitiesProvider interface {
	ModelCapabilities(context.Context, string) (ModelCapabilities, error)
}

type capabilityEntry struct {
	value   ModelCapabilities
	err     error
	expires time.Time
}

var capabilityCache = struct {
	sync.Mutex
	entries map[string]capabilityEntry
}{entries: make(map[string]capabilityEntry)}

// Cache is bounded and includes the credential identity without storing the key.
// Discovery never generates tokens and failure cannot prevent an ordinary chat.
func CachedCapabilities(ctx context.Context, identity string, fetch func(context.Context) (ModelCapabilities, error)) (ModelCapabilities, error) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	capabilityCache.Lock()
	e, ok := capabilityCache.entries[key]
	capabilityCache.Unlock()
	if ok && time.Now().Before(e.expires) {
		return e.value, e.err
	}
	probe, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	v, err := fetch(probe)
	ttl := 10 * time.Minute
	if err != nil {
		ttl = 30 * time.Second
	}
	capabilityCache.Lock()
	if len(capabilityCache.entries) >= 256 {
		clear(capabilityCache.entries)
	}
	capabilityCache.entries[key] = capabilityEntry{v, err, time.Now().Add(ttl)}
	capabilityCache.Unlock()
	return v, err
}

// ParseCapabilities accepts explicit metadata only; no model-name heuristics.
func ParseCapabilities(m map[string]any) ModelCapabilities {
	var out ModelCapabilities
	for _, k := range []string{"context_window", "context_length", "max_model_len", "max_input_tokens"} {
		if n, ok := m[k].(float64); ok && n >= 4096 && n <= 2_000_000 && n == float64(int(n)) {
			if out.ContextWindow == 0 || int(n) < out.ContextWindow {
				out.ContextWindow = int(n)
			}
		}
	}
	if v, ok := m["supports_vision"].(bool); ok {
		out.Vision = &v
	}
	return out
}
