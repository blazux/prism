package docker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecutionCapabilityHasNoOuterRuntimeFallback(t *testing.T) {
	denied := errors.New("lease revoked")
	calls := 0
	m := WithExecution(func(ctx context.Context, command string, input []byte, env map[string]string) (string, error) {
		calls++
		return "", denied
	})
	for _, run := range []func() (string, error){
		func() (string, error) { return m.Exec(context.Background(), "id", time.Second) },
		func() (string, error) { return m.ExecWithEnv(context.Background(), "id", time.Second, nil) },
		func() (string, error) { return m.ExecWithStdin(context.Background(), "id", nil, time.Second, nil) },
	} {
		if _, err := run(); !errors.Is(err, denied) {
			t.Fatalf("unexpected execution %v", err)
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	if _, err := m.run(context.Background(), "docker", "version"); err == nil {
		t.Fatal("outer runtime allowed")
	}
	if _, err := m.serviceCommand(context.Background(), "ps"); err == nil {
		t.Fatal("service runtime allowed")
	}
	if _, err := WithExecution(nil).Exec(context.Background(), "id", time.Second); err == nil {
		t.Fatal("nil capability fell back")
	}
}
