package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commandOutputNativeGoFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "command_output", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Captured with Go 1.26.11 from eval/command-output/tasks/fixtures/
// go-invoice-repair, in the network-disabled evaluation image. Go defers child
// result lines until after the parent: the old active set retained 121 siblings.
func TestCommandOutputGoNativeNestedFailureAndRecovery(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	raw := commandOutputNativeGoFixture(t, "go-invoice-nested-failure.txt")
	got := commandCompactAssertChanged(t, "go test -v -count=1 ./...", raw, "go-test")
	if len(got) > 2000 || strings.Contains(got, "case-119-") || strings.Contains(got, "case-121-") {
		t.Fatalf("buffered quiet siblings retained: raw=%d compact=%d", len(raw), len(got))
	}
	commandCompactAssertKept(t, got,
		"=== RUN   TestInvoiceTotals\n", "=== RUN   TestInvoiceTotals/case-120-fdd35e7e\n",
		"invoice INV-120:", "got 4 cents; want 3 cents", "--- FAIL: TestInvoiceTotals (",
		"--- FAIL: TestInvoiceTotals/case-120-fdd35e7e (",
		"239 passing result lines", "observed results: pass=239 fail=2 skip=0 top-level=1 subtests=240")
	failure := errors.New("exit status 1")
	e := &ToolExecutor{workspaceDir: t.TempDir(), sessionID: "native-go-context"}
	result := e.finishCommandOutput(t.Context(), "go test -v -count=1 ./...", raw, failure)
	commandCompactAssertKept(t, result, "invoice INV-120:", "got 4 cents; want 3 cents", "output compacted; full output:")
	if len(result)*5 > len(commandOutputPreview(raw))*4 || strings.Contains(result, "...[truncated]...") {
		t.Fatalf("diagnostic not visible inside a net reduction: before=%d after=%d", len(commandOutputPreview(raw)), len(result))
	}
	path := commandOutputRecoveryPath(t, result)
	archived, err := os.ReadFile(filepath.Join(e.workspaceDir, path))
	if err != nil || string(archived) != raw+"\n\n[Prism execution error]\n"+failure.Error() {
		t.Fatal("native stdout/error recovery is not byte-exact")
	}
	t.Logf("native Go failure raw=%d preview=%d compact=%d rendered=%d", len(raw), len(commandOutputPreview(raw)), len(got), len(result))
}

// Captured from two synchronized parallel children logging in first/second/
// first order. Their real chatty printer emits NAME switches. All diagnostic
// scopes must survive, even when other tests are reduced.
func TestCommandOutputGoNativeParallelNameSwitches(t *testing.T) {
	raw := commandOutputNativeGoFixture(t, "go-native-parallel-logs.txt")
	got := commandCompactAssertChanged(t, "go test -v -count=1 ./...", raw, "go-test")
	start, end := strings.Index(raw, "=== RUN   TestParallelLogs\n"), strings.LastIndex(raw, "\nPASS\n")
	if start < 0 || end <= start {
		t.Fatal("native parallel fixture lost its runner framing")
	}
	commandCompactAssertKept(t, got, raw[start:end+1], "NATIVE_PARALLEL_FIRST", "NATIVE_PARALLEL_SECOND", "NATIVE_PARALLEL_FIRST_AGAIN",
		"observed results: pass=124 fail=0 skip=0 top-level=2 subtests=122")
}

func TestCommandOutputGoCurrentNameAndMultilineDiagnostic(t *testing.T) {
	output := commandCompactGoSuccesses(24) +
		"=== RUN   TestParent\n=== RUN   TestParent/first\n=== RUN   TestParent/second\n=== NAME  TestParent\n" +
		"    parent_test.go:8: parent line\n        continuation with 日本 /workspace/é.json\n" +
		"--- PASS: TestParent (0.00s)\n    --- PASS: TestParent/first (0.00s)\n    --- PASS: TestParent/second (0.00s)\nPASS\nok\texample.org/current\t0.1s\n"
	got := commandCompactAssertChanged(t, "go test -v", output, "go-test")
	commandCompactAssertKept(t, got, "=== RUN   TestParent\n", "=== NAME  TestParent\n", "    parent_test.go:8: parent line\n", "        continuation with 日本 /workspace/é.json\n", "--- PASS: TestParent (0.00s)\n")
	if strings.Contains(got, "TestParent/first") || strings.Contains(got, "TestParent/second") {
		t.Fatal("parent log retained unrelated quiet children awaiting results")
	}
}

func TestCommandOutputGoRawSiblingLogKeepsAmbiguousContexts(t *testing.T) {
	ambiguous := "=== RUN   TestParent\n=== RUN   TestParent/first\n=== RUN   TestParent/second\n" +
		"unattributed raw stdout from an asynchronous goroutine\n" +
		"--- PASS: TestParent (0.00s)\n    --- PASS: TestParent/first (0.00s)\n    --- PASS: TestParent/second (0.00s)\n"
	output := commandCompactGoSuccesses(24) + ambiguous + "PASS\nok\texample.org/raw\t0.1s\n"
	got := commandCompactAssertChanged(t, "go test -v", output, "go-test")
	commandCompactAssertKept(t, got, ambiguous)
}

func TestCommandOutputGoDiagnosticBetweenResultsKeepsNeighbours(t *testing.T) {
	adjacent := "=== RUN   TestParent\n=== RUN   TestParent/first\n--- PASS: TestParent/first (0.00s)\n" +
		"    delayed_test.go:10: diagnostic after a child result\n" +
		"=== RUN   TestParent/second\n--- PASS: TestParent/second (0.00s)\n--- PASS: TestParent (0.00s)\n"
	output := commandCompactGoSuccesses(24) + adjacent + "PASS\nok\texample.org/neighbours\t0.1s\n"
	got := commandCompactAssertChanged(t, "go test -v", output, "go-test")
	commandCompactAssertKept(t, got, adjacent)
}

func TestCommandOutputGoDistinctOccurrencesAcrossPackagesAndCounts(t *testing.T) {
	block := func(logged bool) string {
		text := commandCompactGoSuccesses(24) + "=== RUN   TestShared\n=== RUN   TestShared/child\n"
		if logged {
			text += "    shared_test.go:3: KEEP_FIRST_OCCURRENCE\n"
		}
		return text + "--- PASS: TestShared (0.00s)\n    --- PASS: TestShared/child (0.00s)\n"
	}
	for _, tc := range []struct{ name, command, output string }{
		{"packages", "go test -v ./...", block(true) + "PASS\nok\texample.org/first\t0.1s\n" + block(false) + "PASS\nok\texample.org/second\t0.1s\n"},
		{"count", "go test -v -count=2 ./...", block(true) + block(false) + "PASS\nok\texample.org/repeated\t0.1s\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := commandCompactAssertChanged(t, tc.command, tc.output, "go-test")
			commandCompactAssertKept(t, got, "KEEP_FIRST_OCCURRENCE", "observed results: pass=52 fail=0 skip=0 top-level=50 subtests=2")
			if strings.Count(got, "=== RUN   TestShared\n") != 1 || strings.Count(got, "--- PASS: TestShared/child (0.00s)") != 1 {
				t.Fatal("one occurrence's diagnostic leaked into another occurrence's retention")
			}
		})
	}
}

func TestCommandOutputGoObservedSkipsAndNestedFailures(t *testing.T) {
	output := commandCompactGoSuccesses(24) +
		"=== RUN   TestParent\n=== RUN   TestParent/skipped\n    skip_test.go:3: skip reason\n--- PASS: TestParent (0.00s)\n    --- SKIP: TestParent/skipped (0.00s)\n" +
		"=== RUN   TestFailure\n--- FAIL: TestFailure (0.00s)\nFAIL\nFAIL\texample.org/statuses\t0.1s\n"
	got := commandCompactAssertChanged(t, "go test -v", output, "go-test")
	commandCompactAssertKept(t, got, "skip reason", "--- SKIP: TestParent/skipped", "--- FAIL: TestFailure", "observed results: pass=25 fail=1 skip=1 top-level=26 subtests=1")
}

func TestCommandOutputGoAmbiguousRunnerSequencesFallBack(t *testing.T) {
	for _, sequence := range []string{
		"=== NAME  TestUnknown\n", "=== CONT  TestUnknown\n", "=== PAUSE TestUnknown\n",
		"=== RUN   TestMissingParent/child\n--- PASS: TestMissingParent/child (0.00s)\n",
		"=== RUN   TestOverlap\n=== RUN   TestOverlap\n--- PASS: TestOverlap (0.00s)\n",
		"=== RUN   TestCompleted\n--- PASS: TestCompleted (0.00s)\n=== CONT  TestCompleted\n",
	} {
		output := commandCompactGoSuccesses(24) + sequence + "PASS\nok\texample.org/ambiguous\t0.1s\n"
		got, filter := compactCommandOutput("go test -v", output)
		if got != output || filter != "" {
			t.Errorf("guessed ambiguous runner context for %q", sequence)
		}
	}
}

func TestCommandOutputGoPanicKeepsPendingScopes(t *testing.T) {
	panicContext := "=== RUN   TestParent\n=== RUN   TestParent/first\n=== RUN   TestParent/second\npanic: native panic marker\n\ngoroutine 9 [running]:\nexample.org/panic_test.go:23 +0x42\n"
	output := commandCompactGoSuccesses(24) + panicContext + "FAIL\texample.org/panic\t0.1s\n"
	got := commandCompactAssertChanged(t, "go test -v", output, "go-test")
	commandCompactAssertKept(t, got, panicContext)
}
