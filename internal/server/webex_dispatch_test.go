package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A slow turn in one space must not hold up another space. This is the whole
// point of moving turns off the Mercury read loop: before, one connection
// served every space of a group and read no further message until the current
// turn returned.
func TestWebexDispatch_SpacesRunConcurrently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	done := make(chan string, 2)
	c := &webexChannel{botID: "bot"}
	c.handle = func(_ context.Context, m webexMessage) {
		<-release // both turns block until released: neither can finish first
		done <- m.RoomID
	}

	c.dispatch(ctx, webexMessage{ID: "1", RoomID: "roomA", PersonID: "alice"})
	c.dispatch(ctx, webexMessage{ID: "2", RoomID: "roomB", PersonID: "bob"})

	// If the spaces were serialized, only one worker would be inside handle and
	// closing release would let just that one through.
	close(release)
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case room := <-done:
			seen[room] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 spaces ran: %v", len(seen), seen)
		}
	}
	if !seen["roomA"] || !seen["roomB"] {
		t.Errorf("both spaces should have run, got %v", seen)
	}
}

// Within one space, order is still guaranteed: a follow-up must never overtake
// the message it follows.
func TestWebexDispatch_OneSpaceStaysOrdered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var order []string
	done := make(chan struct{})
	c := &webexChannel{botID: "bot"}
	c.handle = func(_ context.Context, m webexMessage) {
		time.Sleep(5 * time.Millisecond) // a later message would overtake if unserialized
		mu.Lock()
		order = append(order, m.ID)
		n := len(order)
		mu.Unlock()
		if n == 3 {
			close(done)
		}
	}

	for _, id := range []string{"1", "2", "3"} {
		c.dispatch(ctx, webexMessage{ID: id, RoomID: "roomA", PersonID: "alice"})
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the space's messages")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "1" || order[1] != "2" || order[2] != "3" {
		t.Errorf("messages ran out of order: %v", order)
	}
}

// The bot's own posts come back over Mercury; they must never take a queue slot.
func TestWebexDispatch_IgnoresOwnPosts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &webexChannel{botID: "bot"}
	c.handle = func(_ context.Context, m webexMessage) {
		t.Errorf("own post reached the agent: %+v", m)
	}
	c.dispatch(ctx, webexMessage{ID: "1", RoomID: "roomA", PersonID: "bot"})

	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	if len(c.queues) != 0 {
		t.Errorf("own post created a queue: %v", c.queues)
	}
}

// A full inbox must drop, never block: the Mercury read loop is the caller, and
// blocking it is the failure this whole change removes.
func TestWebexDispatch_FullInboxDropsWithoutBlocking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	defer close(release)
	c := &webexChannel{botID: "bot"}
	c.handle = func(_ context.Context, _ webexMessage) { <-release }

	// Group chatter (no @mention) so the overflow path stays silent instead of
	// posting a notice through the Webex API.
	chatter := webexMessage{RoomID: "roomA", PersonID: "alice", RoomType: "group"}

	returned := make(chan struct{})
	go func() {
		// One in flight, webexRoomQueueCap buffered, the rest dropped.
		for i := 0; i < webexRoomQueueCap+50; i++ {
			c.dispatch(ctx, chatter)
		}
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch blocked on a full inbox")
	}
}

func TestWebexAddressesBot(t *testing.T) {
	c := &webexChannel{botID: "bot"}
	cases := []struct {
		name string
		m    webexMessage
		want bool
	}{
		{"direct message", webexMessage{RoomType: "direct"}, true},
		{"group without mention", webexMessage{RoomType: "group", MentionedPeople: []string{"alice"}}, false},
		{"group with mention", webexMessage{RoomType: "group", MentionedPeople: []string{"alice", "bot"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.addressesBot(tc.m); got != tc.want {
				t.Errorf("addressesBot = %v, want %v", got, tc.want)
			}
		})
	}
}
