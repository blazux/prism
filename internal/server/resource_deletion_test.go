package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"prism/internal/docker"
	"prism/internal/memory"
)

func TestWorkspaceDeletionWaitsForBrowserAndHeadlessJobs(t *testing.T) {
	s := &Server{}
	owner := &Client{user: &memory.User{ID: 8}, sessionID: "u8-board", send: make(chan []byte, 30)}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := s.beginRun(owner, cancel, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	jobCtx, finish, err := s.beginSessionJob(context.Background(), owner.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	otherCtx, otherFinish, err := s.beginSessionJob(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer otherFinish()
	ctx, timeout := context.WithTimeout(context.Background(), 3*time.Second)
	defer timeout()
	type outcome struct {
		release func(bool)
		err     error
	}
	result := make(chan outcome, 1)
	go func() {
		// Service-token identity differs from the browser user, but uses the
		// same physical dashboard and must stop all its tasks.
		release, err := s.quiesceSession(ctx, &Client{sessionID: owner.sessionID})
		result <- outcome{release, err}
	}()
	for _, stopped := range []context.Context{runCtx, jobCtx} {
		select {
		case <-stopped.Done():
		case <-ctx.Done():
			t.Fatal("task not cancelled")
		}
	}
	if otherCtx.Err() != nil {
		t.Fatal("other dashboard cancelled")
	}
	if _, _, err := s.beginSessionJob(context.Background(), owner.sessionID); err == nil {
		t.Fatal("new headless task accepted during deletion")
	}
	if _, err := s.beginRun(&Client{sessionID: owner.sessionID}, func() {}, "fixture", nil); err == nil {
		t.Fatal("new browser task accepted during deletion")
	}
	select {
	case <-result:
		t.Fatal("deletion did not wait for persistence/tools")
	default:
	}
	s.finishRun(run, runCtx)
	select {
	case <-result:
		t.Fatal("deletion did not wait for headless job")
	default:
	}
	finish()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		got.release(true)
	case <-ctx.Done():
		t.Fatal("deletion did not finish")
	}
	if _, _, err := s.beginSessionJob(context.Background(), owner.sessionID); err == nil {
		t.Fatal("deleted dashboard revived by a stale caller")
	}
}

func TestDeletionRetainsDashboardUntilRemoteStopConfirmed(t *testing.T) {
	confirmed := false
	s := &Server{docker: docker.WithExecution(nil).WithExecutionCheck(func(context.Context, string) error {
		if !confirmed {
			return errors.New("command still running")
		}
		return nil
	})}
	c := &Client{sessionID: "board"}
	if _, err := s.quiesceSession(context.Background(), c); err == nil {
		t.Fatal("unsafe deletion admitted")
	}
	if s.deletingSessions[c.sessionID] {
		t.Fatal("failed stop prevented retry")
	}
	confirmed = true
	release, err := s.quiesceSession(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	release(true)
}

func TestWorkspaceDeletionTimeoutReleasesGate(t *testing.T) {
	s := &Server{}
	jobCtx, finish, err := s.beginSessionJob(context.Background(), "board")
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.quiesceSession(ctx, &Client{sessionID: "board"}); err == nil {
		t.Fatal("unfinished job did not block deletion")
	}
	if jobCtx.Err() == nil {
		t.Fatal("job not cancelled")
	}
	s.runsMu.Lock()
	blocked := s.deletingSessions["board"]
	s.runsMu.Unlock()
	if blocked {
		t.Fatal("failed deletion left an unusable workspace")
	}
}
