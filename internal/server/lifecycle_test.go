package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownCancelsAndDrainsRequests(t *testing.T) {
	s := &Server{}
	entered := make(chan struct{})
	exited := make(chan struct{})
	h := s.lifecycleHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(exited)
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("returned before request drained")
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", "/", nil))
	if res.Code != 503 {
		t.Fatal("accepted request after shutdown")
	}
	if s.background(func() { t.Error("late worker ran") }) {
		t.Fatal("accepted late worker")
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestShutdownTimeoutDoesNotClaimDrain(t *testing.T) {
	s := &Server{}
	release := make(chan struct{})
	s.background(func() { <-release })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
	close(release)
	drain, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := s.Shutdown(drain); err != nil {
		t.Fatal(err)
	}
}
func TestEmailWorkerStopsWithRuntime(t *testing.T) {
	s := &Server{}
	s.startEmailRules()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
