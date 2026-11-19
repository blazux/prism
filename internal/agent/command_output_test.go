package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"prism/internal/docker"
	"prism/internal/ollama"
)

func commandOutputIntegrationFixture(detail string) string {
	var out strings.Builder
	for i := 0; i < 180; i++ {
		fmt.Fprintf(&out, "=== RUN   TestOutput%03d\n", i)
		if i == 90 && detail != "" {
			fmt.Fprintf(&out, "    fixture_test.go:91: %s\n--- FAIL: TestOutput%03d (0.00s)\n", detail, i)
		} else {
			fmt.Fprintf(&out, "--- PASS: TestOutput%03d (0.00s)\n", i)
		}
	}
	if detail != "" {
		out.WriteString("FAIL\nFAIL\tprism/fixture\t0.1s\n")
	} else {
		out.WriteString("PASS\nok\tprism/fixture\t0.1s\n")
	}
	return out.String()
}

func commandOutputRecoveryPath(t *testing.T, result string) string {
	t.Helper()
	match := regexp.MustCompile(`/workspace/\.prism-command-output/[0-9a-f-]+\.log`).FindString(result)
	if match == "" {
		t.Fatalf("missing recovery path in %q", result)
	}
	return strings.TrimPrefix(match, "/workspace/")
}

func TestCommandOutputOptInAndRawData(t *testing.T) {
	raw := commandOutputIntegrationFixture("")
	for _, enabled := range []string{"", "1"} {
		for _, programmatic := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%s/raw=%v", enabled, programmatic), func(t *testing.T) {
				t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", enabled)
				dir := t.TempDir()
				e := &ToolExecutor{workspaceDir: dir, rawResults: programmatic}
				got := e.finishCommandOutput(t.Context(), "go test -v ./...", raw, nil)
				if programmatic {
					if got != raw {
						t.Fatal("programmatic stdout changed")
					}
				} else if enabled == "" {
					if got != commandOutputPreview(raw) {
						t.Fatal("disabled experiment changed the baseline")
					}
				} else {
					if len(got) >= len(commandOutputPreview(raw)) {
						t.Fatal("no effective gain")
					}
					name := commandOutputRecoveryPath(t, got)
					data, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil || string(data) != raw {
						t.Fatal("raw stdout not recoverable")
					}
				}
				if programmatic || enabled == "" {
					if _, err := os.Stat(filepath.Join(dir, commandOutputDirectory)); !os.IsNotExist(err) {
						t.Fatal("unnecessary archive created")
					}
				}
			})
		}
	}
}

func TestCommandOutputFallbacks(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	raw := commandOutputIntegrationFixture("")
	for _, tc := range []struct {
		name, command, output string
		execErr               error
	}{
		{"unknown command", "cat fixture.log", raw, nil},
		{"small output", "go test ./...", "ok\tfixture\t0.1s\n", nil},
		{"unknown format", "go test -v ./...", strings.Repeat("keep this diagnostic\n", 600), nil},
		{"binary stdout", "go test -v ./...", raw + "\x00binary", nil},
		{"invalid stderr", "go test -v ./...", raw, errors.New("exit status 1: \xff")},
		{"too large", "go test -v ./...", strings.Repeat(raw, commandOutputMaxBytes/len(raw)+1), nil},
		{"large unchanged error defeats gain", "go test -v ./...", raw, errors.New(strings.Repeat("stderr diagnostic\n", 8000))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &ToolExecutor{workspaceDir: t.TempDir()}
			if got := e.finishCommandOutput(t.Context(), tc.command, tc.output, tc.execErr); got != commandOutputPreview(tc.output) {
				t.Fatal("fallback changed output")
			}
			if _, err := os.Stat(filepath.Join(e.workspaceDir, commandOutputDirectory)); !os.IsNotExist(err) {
				t.Fatal("fallback wrote a recovery file")
			}
		})
	}
	for _, unavailable := range []string{"missing workspace", "file in cache path", "symlink outside volume", "tampered recovery"} {
		t.Run(unavailable, func(t *testing.T) {
			e := &ToolExecutor{workspaceDir: t.TempDir(), sessionID: "fixture"}
			cache := filepath.Join(e.workspaceDir, commandOutputDirectory)
			switch unavailable {
			case "missing workspace":
				e.workspaceDir = filepath.Join(e.workspaceDir, "missing")
			case "file in cache path":
				if err := os.WriteFile(cache, []byte("unrelated file"), 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink outside volume":
				if err := os.Symlink(t.TempDir(), cache); err != nil {
					t.Fatal(err)
				}
			case "tampered recovery":
				first := e.finishCommandOutput(t.Context(), "go test -v ./...", raw, nil)
				path := commandOutputRecoveryPath(t, first)
				if err := os.WriteFile(filepath.Join(e.workspaceDir, path), []byte("edited recovery"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if got := e.finishCommandOutput(t.Context(), "go test -v ./...", raw, nil); got != commandOutputPreview(raw) {
				t.Fatal("compacted without a valid recoverable copy")
			}
		})
	}
}

func TestCommandOutputExecutionAndRecovery(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	dir := t.TempDir()
	failure := errors.New("exit status 7: original stderr")
	raw := commandOutputIntegrationFixture("CENTRAL_FAILURE: expected 42, got 41")
	var commandCount int
	e := NewToolExecutor(docker.WithExecution(func(ctx context.Context, command string, _ []byte, env map[string]string) (string, error) {
		commandCount++
		if strings.Contains(command, "go test") {
			if command != "cd /workspace && go test -v ./..." || env["PRISM_SESSION"] != "fixture" {
				t.Fatal("original command or environment changed")
			}
			return raw, failure
		}
		// Only the existing recovery command executes locally, in a test directory.
		local := strings.ReplaceAll(command, "/workspace", dir)
		data, err := exec.CommandContext(ctx, "bash", "-c", local).CombinedOutput()
		return string(data), err
	}), dir, t.TempDir(), "", "")
	e.SetSessionID("fixture")
	got, _, err := e.Execute(t.Context(), "exec_command", json.RawMessage(`{"command":"go test -v ./..."}`))
	if err != failure || !errors.Is(err, failure) || commandCount != 1 {
		t.Fatal("execution was retried or error identity changed")
	}
	if !strings.Contains(got, "CENTRAL_FAILURE") || !strings.Contains(got, "TestOutput090") {
		t.Fatal("failure diagnostic lost")
	}
	path := commandOutputRecoveryPath(t, got)
	data, readErr := os.ReadFile(filepath.Join(dir, path))
	if readErr != nil || string(data) != raw+"\n\n[Prism execution error]\n"+failure.Error() {
		t.Fatal("stdout/error recovery differs")
	}
	if result, readErr := e.readFile(path); readErr != nil || !strings.HasPrefix(result, raw[:200]) {
		t.Fatal("existing read_file cannot open recovery")
	}
	args, _ := json.Marshal(map[string]string{"command": "grep -F CENTRAL_FAILURE /workspace/" + path})
	full, _, err := e.Execute(t.Context(), "exec_command", args)
	if err != nil || !strings.Contains(full, "CENTRAL_FAILURE") {
		t.Fatal("existing exec_command cannot recover diagnostics")
	}
}

func TestCommandOutputStableIdentityAndLoopProgress(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	e := &ToolExecutor{workspaceDir: t.TempDir(), sessionID: "fixture"}
	raw := commandOutputIntegrationFixture("")
	first := e.finishCommandOutput(t.Context(), "go test -v ./...", raw, nil)
	if again := e.finishCommandOutput(t.Context(), "go test -v ./...", raw, nil); again != first {
		t.Fatal("same result changed its historical representation")
	}
	// A duration is genuinely new information even if success names are removed.
	changed := strings.Replace(raw, "(0.00s)", "(0.01s)", 1)
	second := e.finishCommandOutput(t.Context(), "go test -v ./...", changed, nil)
	if commandOutputRecoveryPath(t, first) == commandOutputRecoveryPath(t, second) {
		t.Fatal("distinct raw results share identity")
	}
	call := ollama.ToolCall{Function: ollama.ToolCallFunction{Name: "exec_command", Arguments: json.RawMessage(`{"command":"go test -v ./..."}`)}}
	var loop toolLoopDetector
	for i := 0; i < 6; i++ {
		varied := strings.Replace(raw, "(0.00s)", fmt.Sprintf("(0.%02ds)", i), 1)
		result := e.finishCommandOutput(t.Context(), "go test -v ./...", varied, nil)
		if loop.observe(call, result, nil) != toolLoopNone {
			t.Fatal("filter made new raw observations look repeated")
		}
	}
	var repeated toolLoopDetector
	for i := 0; i < 3; i++ {
		repeated.observe(call, first, nil)
	}
	if repeated.warning() == "" {
		t.Fatal("identical output stopped being detected")
	}
}

func TestCommandOutputCacheLimitsAndConcurrentReuse(t *testing.T) {
	e := &ToolExecutor{workspaceDir: t.TempDir()}
	content := []byte("complete stdout")
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := e.storeCommandOutput(commandOutputName("same", "command", "stdout", ""), content); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	for scope := 0; scope < 5; scope++ {
		for i := 0; i < 36; i++ {
			name := commandOutputName(fmt.Sprint(scope), "command", fmt.Sprint(i), "")
			if err := e.storeCommandOutput(name, content); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(e.workspaceDir, commandOutputDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > commandOutputTotal {
		t.Fatal("global recovery cache unbounded")
	}
	counts := map[string]int{}
	for _, entry := range entries {
		counts[entry.Name()[:16]]++
	}
	for _, n := range counts {
		if n > commandOutputPerScope {
			t.Fatal("session recovery cache unbounded")
		}
	}
	if err := e.storeCommandOutput(commandOutputName("scope", "command", "stdout", ""), make([]byte, commandOutputMaxBytes+1)); err == nil {
		t.Fatal("oversized recovery accepted")
	}
}

func TestCompactOutputPreviewUTF8(t *testing.T) {
	for _, output := range []string{strings.Repeat("é", 6000), strings.Repeat("界", 4000), strings.Repeat("😀", 3000)} {
		preview := compactOutputPreview(output)
		if !utf8.ValidString(preview) || len(preview) > 8019 {
			t.Fatal("compacted preview split UTF-8 or exceeded budget")
		}
	}
}

type commandOutputConversationBackend struct {
	requests    [][]ollama.Message
	requestTool bool
}

func (b *commandOutputConversationBackend) Chat(_ context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.requests = append(b.requests, append([]ollama.Message(nil), req.Messages...))
	if b.requestTool && len(b.requests) == 1 {
		out <- ollama.StreamEvent{ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{
			Name: "exec_command", Arguments: json.RawMessage(`{"command":"go test -v ./..."}`),
		}}}, Done: true, DoneReason: "tool_calls"}
		return
	}
	out <- ollama.StreamEvent{Content: "The test failure is available in the result.", Done: true, DoneReason: "stop"}
}
func (*commandOutputConversationBackend) Ping(context.Context) error { return nil }
func (*commandOutputConversationBackend) ListModels(context.Context) ([]string, error) {
	return nil, nil
}
func (*commandOutputConversationBackend) ContextBudgetChars() int { return 0 }

func TestCommandOutputConversationAndStoredReplay(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	store := resourceDatabase(t)
	raw := commandOutputIntegrationFixture("REPLAY_FAILURE: expected 42, got 41")
	dir := t.TempDir()
	executions := 0
	finished := make(chan struct{}, 2)
	manager := docker.WithExecution(func(_ context.Context, command string, _ []byte, _ map[string]string) (string, error) {
		if strings.Contains(command, "go test") {
			executions++
			return raw, errors.New("exit status 1: fixture stderr")
		}
		if strings.Contains(command, "command -v git") {
			finished <- struct{}{}
		}
		return "no", nil
	})
	e := NewToolExecutor(manager, dir, t.TempDir(), "", "")
	e.SetMemoryStore(store)
	backend := &commandOutputConversationBackend{requestTool: true}
	ag := New(backend, e, "fixture", store, "")
	ag.SetSession("compact-replay", "")
	events := make(chan Event, 100)
	ag.Chat(t.Context(), "Run the tests", nil, events)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("workspace snapshot did not finish")
	}
	if executions != 1 || len(backend.requests) != 2 {
		t.Fatal("unexpected command or model-call count")
	}
	var first string
	for _, message := range backend.requests[1] {
		if message.Role == "tool" {
			first = message.Content
		}
	}
	if !strings.Contains(first, "REPLAY_FAILURE") || !strings.Contains(first, "Error: exit status 1") || !strings.Contains(first, commandOutputDirectory) || strings.Contains(first, "TestOutput179") {
		t.Fatal("model did not receive the compacted diagnostic and recovery path")
	}
	close(events)
	var eventOutput string
	for event := range events {
		if event.Type == "tool_result" {
			if !event.IsError {
				t.Fatal("failed command shown as successful")
			}
			eventOutput = event.Output
		}
	}
	if eventOutput != first {
		t.Fatal("UI/model representations diverged")
	}
	rows, err := store.LoadHistory(t.Context(), "compact-replay")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	for _, row := range rows {
		if row.Role == "tool" {
			stored = row.Content
		}
	}
	if stored != first {
		t.Fatal("persisted result differs from model result")
	}
	usage, err := store.RecentUsageEvents(t.Context(), "command_output", 5)
	if err != nil || len(usage) != 1 {
		t.Fatal("missing command-output measurement")
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(usage[0].Meta, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["mode"] != "compact" || int(meta["returned_bytes"].(float64)) != len(first) {
		t.Fatal("measurement differs from actual model content")
	}
	for _, private := range []string{"REPLAY_FAILURE", "=== RUN", "go test -v", commandOutputDirectory} {
		if strings.Contains(string(usage[0].Meta), private) {
			t.Fatal("measurement contains command or result content")
		}
	}
	// Replay uses the saved representation even if an operator disables the
	// experiment later; it neither expands old results nor reruns the command.
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "")
	nextBackend := &commandOutputConversationBackend{}
	next := New(nextBackend, e, "fixture", store, "")
	next.SetSession("compact-replay", "")
	next.loadHistoryFromDB(t.Context())
	next.Chat(t.Context(), "Explain that failure", nil, make(chan Event, 100))
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("workspace snapshot did not finish")
	}
	if executions != 1 || len(nextBackend.requests) != 1 {
		t.Fatal("replay repeated the command")
	}
	var replayed string
	for _, message := range nextBackend.requests[0] {
		if message.Role == "tool" {
			replayed = message.Content
		}
	}
	if replayed != first {
		t.Fatal("replay rewrote the compacted result")
	}
}
