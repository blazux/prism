package server

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Ingestion is synchronous: the browser holds one long POST while the server
// parses, embeds and stores. Nothing could report where it was, so a large
// manual looked frozen for minutes. The handler now publishes its progress here
// and the UI polls /api/rag/upload/progress while its own upload is in flight.
//
// State is in-memory and per-process: a restart loses it, which is fine — the
// upload dies with the process anyway.

type ingestProgress struct {
	Stage    string    `json:"stage"` // parsing | embedding | storing | done | failed
	Done     int       `json:"done"`
	Total    int       `json:"total"`
	Rate     float64   `json:"rate"`    // chunks per second, 0 until measured
	ETASecs  int       `json:"etaSecs"` // remaining seconds, 0 when unknown
	Error    string    `json:"error"`   // set when Stage == "failed"
	Updated  time.Time `json:"-"`
	finished time.Time
}

type ingestTracker struct {
	mu   sync.RWMutex
	jobs map[string]*ingestProgress
}

func newIngestTracker() *ingestTracker {
	return &ingestTracker{jobs: make(map[string]*ingestProgress)}
}

// ingestKey identifies one upload. Two people uploading the same filename into
// the same collection would share a key — they are also overwriting the same
// document, so sharing progress is the honest thing to show.
func ingestKey(collection, filename string) string {
	return strings.ToLower(collection) + "\x00" + strings.ToLower(filename)
}

func (t *ingestTracker) set(collection, filename string, p ingestProgress) {
	if t == nil {
		return
	}
	p.Updated = time.Now()
	if p.Stage == "done" || p.Stage == "failed" {
		p.finished = p.Updated
	}
	t.mu.Lock()
	t.jobs[ingestKey(collection, filename)] = &p
	t.sweepLocked()
	t.mu.Unlock()
}

func (t *ingestTracker) get(collection, filename string) (ingestProgress, bool) {
	if t == nil {
		return ingestProgress{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.jobs[ingestKey(collection, filename)]
	if !ok {
		return ingestProgress{}, false
	}
	return *p, true
}

// sweepLocked drops finished jobs after a grace period (the UI needs one last
// poll to see "done") and stale ones whose handler died without a final state.
func (t *ingestTracker) sweepLocked() {
	now := time.Now()
	for k, p := range t.jobs {
		switch {
		case !p.finished.IsZero() && now.Sub(p.finished) > 30*time.Second:
			delete(t.jobs, k)
		case now.Sub(p.Updated) > 15*time.Minute:
			delete(t.jobs, k)
		}
	}
}

// handleRAGUploadProgress reports where an in-flight ingestion stands. The
// caller must be allowed to read that collection's scope, exactly like the
// upload itself.
func (s *Server) handleRAGUploadProgress(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := s.ragScopeForRequest(r)
	if !ok {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	collection := sanitizeName(r.URL.Query().Get("collection"))
	filename := r.URL.Query().Get("filename")
	if collection == "" || filename == "" {
		jsonError(w, "collection and filename required", http.StatusBadRequest)
		return
	}
	p, found := s.ingest.get(scopeCollection(scope, collection), filename)
	if !found {
		writeJSON(w, map[string]interface{}{"found": false})
		return
	}
	writeJSON(w, map[string]interface{}{
		"found":   true,
		"stage":   p.Stage,
		"done":    p.Done,
		"total":   p.Total,
		"rate":    p.Rate,
		"etaSecs": p.ETASecs,
		"error":   p.Error,
	})
}
