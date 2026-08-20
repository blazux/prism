package server

import (
	"fmt"
	"testing"
	"time"
)

// The missed-chatter buffer must cap per space (only the most recent kept),
// drain once (replayed context must not reappear on the next @mention), and
// keep spaces independent.
func TestWebexMissedBuffer(t *testing.T) {
	c := &webexChannel{}
	for i := 0; i < webexMissedCap+5; i++ {
		c.rememberMissed("roomA", missedWebexMsg{at: time.Now(), name: "alice", text: fmt.Sprintf("msg %d", i)})
	}
	c.rememberMissed("roomB", missedWebexMsg{at: time.Now(), name: "bob", text: "autre space"})

	got := c.drainMissed("roomA")
	if len(got) != webexMissedCap {
		t.Fatalf("drain roomA = %d messages, want cap %d", len(got), webexMissedCap)
	}
	if got[0].text != "msg 5" || got[len(got)-1].text != fmt.Sprintf("msg %d", webexMissedCap+4) {
		t.Errorf("ring kept the wrong window: first=%q last=%q", got[0].text, got[len(got)-1].text)
	}
	if again := c.drainMissed("roomA"); len(again) != 0 {
		t.Errorf("second drain should be empty, got %d", len(again))
	}
	if b := c.drainMissed("roomB"); len(b) != 1 || b[0].name != "bob" {
		t.Errorf("roomB buffer clobbered: %v", b)
	}
}
