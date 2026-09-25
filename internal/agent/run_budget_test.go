package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSharedModelBudget(t *testing.T) {
	ctx := withModelBudget(context.Background(), 7)
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if consumeModelCall(withModelBudget(ctx, 100)) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 7 {
		t.Fatal("child expanded shared budget", accepted.Load())
	}
	if err := consumeModelCall(context.Background()); err != nil {
		t.Fatal("standalone calls changed")
	}
}
