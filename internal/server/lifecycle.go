package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// lifecycle gates admission before waiting: no Add can race a shutdown Wait.
type lifecycle struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	stopped bool
	workers sync.WaitGroup
	done    chan struct{}
}

func (s *Server) runtimeContext() context.Context {
	s.life.mu.Lock()
	defer s.life.mu.Unlock()
	if s.life.ctx == nil {
		s.life.ctx, s.life.cancel = context.WithCancel(context.Background())
	}
	return s.life.ctx
}
func (s *Server) admit() bool {
	s.life.mu.Lock()
	defer s.life.mu.Unlock()
	if s.life.stopped {
		return false
	}
	s.life.workers.Add(1)
	return true
}
func (s *Server) background(fn func()) bool {
	if !s.admit() {
		return false
	}
	go func() { defer s.life.workers.Done(); fn() }()
	return true
}
func (s *Server) lifecycleHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.admit() {
			http.Error(w, "server stopping", http.StatusServiceUnavailable)
			return
		}
		defer s.life.workers.Done()
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(s.runtimeContext(), cancel)
		defer stop()
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Initialize starts this application's resources once, without opening a port.
// A failed initialization is terminal; construct a fresh application to retry.
func (s *Server) Initialize(ctx context.Context) error {
	_, err := s.initializeOnce(ctx)
	return err
}

func (s *Server) initializeOnce(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.life.mu.Lock()
	if s.life.started || s.life.stopped {
		s.life.mu.Unlock()
		return false, errors.New("application already initialized or stopped")
	}
	s.life.started = true
	s.life.workers.Add(1)
	s.life.mu.Unlock()
	defer s.life.workers.Done()
	runtime := s.runtimeContext()
	// Cancelling initialization's parent also cancels background work.
	stop := context.AfterFunc(ctx, func() { s.life.mu.Lock(); s.life.cancel(); s.life.mu.Unlock() })
	if err := s.initialize(runtime); err != nil {
		stop()
		s.life.mu.Lock()
		s.life.cancel()
		s.life.mu.Unlock()
		return true, err
	}
	// Stop this callback once the application is stopped, avoiding a retained parent.
	context.AfterFunc(runtime, func() { stop() })
	return true, runtime.Err()
}

// Shutdown rejects new requests, cancels running work, and waits for tracked
// requests/workers before closing their stores. A timeout is not a clean drain.
func (s *Server) Shutdown(ctx context.Context) error {
	s.runtimeContext()
	s.life.mu.Lock()
	if !s.life.stopped {
		s.life.stopped = true
		s.life.cancel()
		s.life.done = make(chan struct{})
		go func() {
			s.life.workers.Wait()
			s.mu.Lock()
			ms := s.memStore
			s.memStore = nil
			s.mu.Unlock()
			s.ragMu.Lock()
			if s.ragStore != nil {
				s.ragStore.Close()
				s.ragStore = nil
			}
			s.ragMu.Unlock()
			if ms != nil {
				ms.Close()
			}
			close(s.life.done)
		}()
	}
	done := s.life.done
	s.life.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Serve owns the supplied listener. External hosts can instead Initialize,
// mount Handler, stop admitting HTTP traffic, then call Shutdown.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()
	if owned, err := s.initializeOnce(ctx); err != nil {
		if !owned {
			return err
		}
		drain, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return errors.Join(err, s.Shutdown(drain))
	}
	handler, err := s.Handler()
	if err != nil {
		drain, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return errors.Join(err, s.Shutdown(drain))
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	result := make(chan error, 1)
	go func() { result <- httpServer.Serve(listener) }()
	select {
	case err = <-result:
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shutdownErr := s.Shutdown(drain)
	httpErr := httpServer.Shutdown(drain)
	if httpErr != nil {
		_ = httpServer.Close()
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, shutdownErr, httpErr)
}
