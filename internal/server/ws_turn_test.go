package server

import (
	"context"
	"testing"
	"time"
)

func TestCancelActiveDrainsTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c := &Client{cancelFn: cancel, turnDone: done}
	returned := make(chan struct{})
	go func() { c.cancelActive(); close(returned) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("turn not cancelled")
	}
	select {
	case <-returned:
		t.Fatal("returned before old turn drained")
	default:
	}
	close(done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("cancel did not complete")
	}
	c.cancelActive() // repeated cancellation is safe
}
