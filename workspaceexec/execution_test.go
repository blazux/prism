package workspaceexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Run the exact embedded supervisor, not a mock of its process signalling.
func invocation(t *testing.T, ctx context.Context, command string, input []byte, env []string) (string, string, error) {
	t.Helper()
	id := ID()
	cmd := exec.Command(Arguments(id)[0], Arguments(id)[1:]...)
	cmd.Env = append(os.Environ(), env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := Run(ctx, cmd, id, "/bin/bash", command, input)
	return out.String(), stderr.String(), err
}

func TestForegroundPreservesIOEnvironmentAndExit(t *testing.T) {
	input := append([]byte{'\x00', '\xff', '\n'}, bytes.Repeat([]byte("payload"), 50000)...)
	out, stderr, err := invocation(t, context.Background(), "printf '%s' \"$PRISM_TEST_ENV\" >&2; cat; exit 37", input, []string{"PRISM_TEST_ENV=hello ' $ literal"})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 37 || errors.Is(err, ErrUnconfirmed) {
		t.Fatalf("exit status: %v", err)
	}
	if !bytes.Equal([]byte(out), input) || stderr != "hello ' $ literal" {
		t.Fatalf("IO changed: stdout %d stderr %q", len(out), stderr)
	}
}

func TestCancelStopsGrandchildrenBeforeReturn(t *testing.T) {
	for _, stubborn := range []bool{false, true} {
		t.Run(map[bool]string{false: "TERM", true: "KILL"}[stubborn], func(t *testing.T) {
			dir := t.TempDir()
			ready, late := filepath.Join(dir, "ready"), filepath.Join(dir, "late")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			trap := ""
			if stubborn {
				trap = "trap '' TERM; "
			}
			command := trap + "(sleep 2; echo late > '" + late + "') & echo ready > '" + ready + "'; wait"
			done := make(chan error, 1)
			go func() { _, _, err := invocation(t, ctx, command, nil, nil); done <- err }()
			waitFile(t, ready)
			started := time.Now()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnconfirmed) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("command did not stop")
			}
			if time.Since(started) > 1800*time.Millisecond {
				t.Fatal("stop too slow")
			}
			time.Sleep(2100 * time.Millisecond)
			if _, err := os.Stat(late); !os.IsNotExist(err) {
				t.Fatal("grandchild wrote after cancellation")
			}
		})
	}
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("command did not start")
}

func TestCancellationDoesNotStopAnotherExecution(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, _, err := invocation(t, ctx, "echo ready > '"+ready+"'; sleep 90", nil, nil); first <- err }()
	waitFile(t, ready)
	second := make(chan string, 1)
	go func() {
		out, _, err := invocation(t, context.Background(), "sleep 0.4; echo unaffected", nil, nil)
		if err != nil {
			out = err.Error()
		}
		second <- out
	}()
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnconfirmed) {
		t.Fatal(err)
	}
	if got := <-second; got != "unaffected\n" {
		t.Fatal(got)
	}
}

func TestTimeoutStopsCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := invocation(t, ctx, "sleep 90", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrUnconfirmed) {
		t.Fatal(err)
	}
}

func TestLostSupervisorFailsClosedAndFenceCanRetry(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	err := Run(context.Background(), cmd, ID(), "/bin/bash", "sleep 90", nil)
	if !errors.Is(err, ErrUnconfirmed) {
		t.Fatal("missing acknowledgement accepted", err)
	}
	var fence Fence
	available := false
	fence.Remember("board", "command", func(context.Context) error {
		if !available {
			return errors.New("runtime unavailable")
		}
		return nil
	})
	if err := fence.Check(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	if err := fence.Check(context.Background(), "board"); !errors.Is(err, ErrUnconfirmed) {
		t.Fatal(err)
	}
	available = true
	if err := fence.Check(context.Background(), "board"); err != nil {
		t.Fatal(err)
	}
}

func TestAcknowledgementsArePrivateAndSurviveSplitWrites(t *testing.T) {
	id := ID()
	var output bytes.Buffer
	w := markerWriter{id: id, dst: &output}
	for _, p := range []byte("raw\x00stderr\x1ePRISM_EXEC_STOPPED:" + id + "\x1ftail") {
		if _, err := w.Write([]byte{p}); err != nil {
			t.Fatal(err)
		}
	}
	w.flush()
	if !w.stopped || output.String() != "raw\x00stderrtail" {
		t.Fatalf("protocol leaked or changed stderr: %q", output.String())
	}
}
