package server

import (
	"sync"
	"testing"
	"time"
)

// The tracker is written by the upload handler and read by the polling UI at
// the same time. A data race here would be invisible until it corrupted a map.
func TestIngestTrackerConcurrentAccess(t *testing.T) {
	tr := newIngestTracker()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			tr.set("col", "doc.pdf", ingestProgress{Stage: "embedding", Done: n, Total: 50})
		}(i)
		go func() { defer wg.Done(); tr.get("col", "doc.pdf") }()
	}
	wg.Wait()
	if p, ok := tr.get("col", "doc.pdf"); !ok || p.Total != 50 {
		t.Fatalf("progress lost: %+v ok=%v", p, ok)
	}
}

// Two documents ingesting at once must not shadow each other.
func TestIngestTrackerKeysAreIndependent(t *testing.T) {
	tr := newIngestTracker()
	tr.set("col", "a.pdf", ingestProgress{Stage: "embedding", Done: 1, Total: 10})
	tr.set("col", "b.pdf", ingestProgress{Stage: "storing", Done: 9, Total: 9})
	a, _ := tr.get("col", "a.pdf")
	b, _ := tr.get("col", "b.pdf")
	if a.Stage != "embedding" || b.Stage != "storing" {
		t.Errorf("keys collided: a=%s b=%s", a.Stage, b.Stage)
	}
	if _, ok := tr.get("other", "a.pdf"); ok {
		t.Error("collection is not part of the key")
	}
}

// A finished job must survive long enough for the UI's last poll, then vanish
// so the map does not grow for the life of the process.
func TestIngestTrackerSweepsFinishedJobs(t *testing.T) {
	tr := newIngestTracker()
	tr.set("col", "old.pdf", ingestProgress{Stage: "done"})
	if _, ok := tr.get("col", "old.pdf"); !ok {
		t.Fatal("finished job dropped immediately — the UI would never see 'done'")
	}
	// Age it past the grace period, then trigger a sweep with another write.
	tr.mu.Lock()
	tr.jobs[ingestKey("col", "old.pdf")].finished = time.Now().Add(-31 * time.Second)
	tr.mu.Unlock()
	tr.set("col", "new.pdf", ingestProgress{Stage: "parsing"})
	if _, ok := tr.get("col", "old.pdf"); ok {
		t.Error("finished job never swept — the map leaks")
	}
	if _, ok := tr.get("col", "new.pdf"); !ok {
		t.Error("sweep removed the live job")
	}
}

// A nil tracker (tests build &Server{} directly) must not panic.
func TestIngestTrackerNilSafe(t *testing.T) {
	var tr *ingestTracker
	tr.set("c", "f", ingestProgress{Stage: "parsing"})
	if _, ok := tr.get("c", "f"); ok {
		t.Error("nil tracker reported a job")
	}
}
