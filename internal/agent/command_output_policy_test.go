package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/docker"
)

// Captured from the original Go name-retrieval evaluation fixture. Its full
// stdout fits in the existing preview and includes the exact requested name.
//
//go:embed testdata/command_output/go-invoice-names-pass.txt
var commandOutputCompleteGoNames string

func TestCommandOutputCompleteResultsKeepExactDetails(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	const requested = "TestInvoiceTotals/case-018-46f64dac"
	if strings.Count(commandOutputCompleteGoNames, requested) != 2 {
		t.Fatal("fixture must expose the requested RUN and PASS identity")
	}
	for _, size := range []int{len(commandOutputCompleteGoNames), commandOutputPreviewLimit} {
		for _, executionError := range []error{nil, errors.New("exit status 17: original error")} {
			output := commandOutputCompleteGoNames + strings.Repeat("\n", size-len(commandOutputCompleteGoNames))
			e := &ToolExecutor{workspaceDir: t.TempDir(), sessionID: "complete-output"}
			if got := e.finishCommandOutput(t.Context(), "go test -v ./...", output, executionError); got != output {
				t.Fatal("complete stdout no longer exposes every exact detail")
			}
			if _, err := os.Lstat(filepath.Join(e.workspaceDir, commandOutputDirectory)); !os.IsNotExist(err) {
				t.Fatal("complete stdout needlessly created an archive")
			}
		}
	}
}

func TestCommandOutputBeyondPreviewRemainsRecoverable(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	output := commandOutputCompleteGoNames + strings.Repeat("\n", commandOutputPreviewLimit+1-len(commandOutputCompleteGoNames))
	e := &ToolExecutor{workspaceDir: t.TempDir(), sessionID: "long-output"}
	got := e.finishCommandOutput(t.Context(), "go test -v ./...", output, nil)
	if len(got) >= len(commandOutputPreview(output)) {
		t.Fatal("oversized recognized output no longer benefits from compaction")
	}
	path := commandOutputRecoveryPath(t, got)
	raw, err := os.ReadFile(filepath.Join(e.workspaceDir, path))
	if err != nil || string(raw) != output {
		t.Fatal("full stdout changed during archival")
	}
}

func TestCommandOutputWrappedRunnerExecution(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	failure := errors.New("exit status 7: original execution error")
	for _, tc := range []struct {
		name, command, stdout, diagnostic string
		err                               error
	}{
		{"merged error", "go test -v ./... 2>&1", commandOutputIntegrationFixture("CENTRAL_ERROR"), "CENTRAL_ERROR", failure},
		{"go tee status", `cd /workspace && go test -v ./... 2>&1 | tee /tmp/tests.log; echo "EXIT:$?"`, commandOutputIntegrationFixture("") + "EXIT:0\n", "EXIT:0\n", nil},
		{"node status", `node --test fixture.test.js 2>&1; echo "exit=$?"`, commandOutputSyntheticTAP(false) + "exit=0\n", "exit=0\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			manager := docker.WithExecution(func(_ context.Context, command string, _ []byte, _ map[string]string) (string, error) {
				calls++
				if command != "cd /workspace && "+tc.command {
					t.Fatal("formatter changed the original command")
				}
				return tc.stdout, tc.err
			})
			e := NewToolExecutor(manager, t.TempDir(), t.TempDir(), "", "")
			e.SetSessionID("wrapped-runner")
			args, _ := json.Marshal(map[string]string{"command": tc.command})
			got, _, err := e.Execute(t.Context(), "exec_command", args)
			if calls != 1 || err != tc.err || !strings.Contains(got, tc.diagnostic) {
				t.Fatal("execution, error identity or diagnostic/footer changed")
			}
			path := commandOutputRecoveryPath(t, got)
			raw, readErr := os.ReadFile(filepath.Join(e.workspaceDir, path))
			expected := tc.stdout
			if tc.err != nil {
				expected += "\n\n[Prism execution error]\n" + tc.err.Error()
			}
			if readErr != nil || string(raw) != expected {
				t.Fatal("archive differs from original stdout and execution error")
			}
		})
	}
}
