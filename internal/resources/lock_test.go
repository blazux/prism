package resources

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLockContextCancelsWithoutWaitingForMutation(t *testing.T) {
	root := t.TempDir()
	unlock := Lock(root)
	defer unlock()
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		release, err := LockContext(ctx, root)
		if release != nil {
			release()
		}
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled waiter acquired mutation lock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request stayed queued behind mutation")
	}
}

func TestLockAndLockContextShareExclusion(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	count := 0
	for i := range 20 {
		wg.Go(func() {
			var unlock func()
			if i%2 == 0 {
				unlock = Lock(root)
			} else {
				var err error
				unlock, err = LockContext(t.Context(), root)
				if err != nil {
					t.Error(err)
					return
				}
			}
			defer unlock()
			count++
		})
	}
	wg.Wait()
	if count != 20 {
		t.Fatalf("lost concurrent mutations: %d", count)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if release, err := LockContext(ctx, root); release != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("already-cancelled request entered the critical section")
	}
}
