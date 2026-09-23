package server

import (
	"context"
	"errors"
	"sync"

	"prism/internal/memory"
)

// PersonalData owns only an isolated storage connection and its request leases.
// It has no application, HTTP server, model defaults or execution resources.
type PersonalData struct {
	owner    string
	store    *memory.Store
	mu       sync.Mutex
	closed   bool
	requests sync.WaitGroup
	done     chan struct{}
}

func OpenPersonalData(ctx context.Context, owner, dsn string, key []byte) (*PersonalData, error) {
	if owner == "" || dsn == "" || len(key) != 32 {
		return nil, errors.New("invalid personal data configuration")
	}
	ms, err := memory.NewStore(ctx, dsn, append([]byte(nil), key...), false)
	if err != nil {
		return nil, err
	}
	return &PersonalData{owner: owner, store: ms}, nil
}
func (d *PersonalData) acquire(owner string) (*memory.Store, func(), bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.owner != owner {
		return nil, func() {}, false
	}
	d.requests.Add(1)
	return d.store, d.requests.Done, true
}
func (d *PersonalData) Close(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		d.done = make(chan struct{})
		go func() { d.requests.Wait(); d.store.Close(); close(d.done) }()
	}
	done := d.done
	d.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
