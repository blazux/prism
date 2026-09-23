package docker

import (
	"context"
	"os/exec"
	"testing"
)

func TestBoundWorkspaceServicesQuoteAndStdin(t *testing.T) {
	var got string
	m := WithExecution(func(ctx context.Context, command string, input []byte, env map[string]string) (string, error) {
		got = command
		// Replace only the command executable with printf to verify exact shell quoting.
		return string(input), nil
	}).WithWorkspaceServices(t.TempDir())
	out, err := m.serviceInput(context.Background(), []byte("compose snapshot"), "exec", "prism-svc-test", "sh", "-c", "echo 'hi'; echo $(false)")
	if err != nil || out != "compose snapshot" {
		t.Fatal(out, err)
	}
	if err := exec.Command("sh", "-n", "-c", got).Run(); err != nil {
		t.Fatal(got, err)
	}
	if _, err := m.serviceCommand(context.Background(), "ps"); err == nil {
		t.Fatal("outer runtime accessible")
	}
}
