package agent

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func commandCompactGoSuccesses(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "=== RUN   TestQuiet%03d\n--- PASS: TestQuiet%03d (0.00s)\n", i, i)
	}
	return b.String()
}

func commandCompactTAPSuccesses(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "# Subtest: quiet case %03d\nok %d - quiet case %03d\n  ---\n  duration_ms: 0.125\n  type: 'test'\n  ...\n", i, i, i)
	}
	return b.String()
}

func commandCompactTAPSummary(top, tests, pass, fail int) string {
	return fmt.Sprintf("1..%d\n# tests %d\n# suites 0\n# pass %d\n# fail %d\n# cancelled 0\n# skipped 0\n# todo 0\n# duration_ms 30.25\n", top, tests, pass, fail)
}

func commandCompactAssertKept(t *testing.T, got string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(got, value) {
			t.Errorf("lost diagnostic/context %q in:\n%s", value, got)
		}
	}
}

func commandCompactAssertChanged(t *testing.T, command, output, wantFilter string) string {
	t.Helper()
	got, filter := compactCommandOutput(command, output)
	if filter != wantFilter || got == output || len(got) >= len(output) {
		t.Fatalf("expected a smaller %s result; filter=%q raw=%d compact=%d", wantFilter, filter, len(output), len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("compacted output is not valid UTF-8")
	}
	return got
}

func TestCompactCommandOutputGoQuietSuccess(t *testing.T) {
	output := commandCompactGoSuccesses(24) + "=== RUN   TestÉté_日本\n--- PASS: TestÉté_日本 (0.01s)\nPASS\nok  \texample.org/équipe/tests\t0.123s\n"
	got := commandCompactAssertChanged(t, "go test -v -count=1 ./...", output, "go-test")
	commandCompactAssertKept(t, got, "25 passing result lines", "25 progress lines", "1 PASS markers", "ok  \texample.org/équipe/tests\t0.123s\n")
	if strings.Contains(got, "TestQuiet") || strings.Contains(got, "TestÉté") {
		t.Fatal("quiet success names were not replaced by counters")
	}
}

func TestCompactCommandOutputGoFailureAndLogs(t *testing.T) {
	diagnostic := "    été_test.go:42: attendu 日本, reçu été; fixture /workspace/données/input.json\n"
	output := commandCompactGoSuccesses(12) +
		"=== RUN   TestÉchec_日本\n" + diagnostic + "--- FAIL: TestÉchec_日本 (0.01s)\n" +
		"=== RUN   TestPassingWithLog\n    logs_test.go:8: warning: external service unavailable\n--- PASS: TestPassingWithLog (0.00s)\n" +
		commandCompactGoSuccesses(12) + "FAIL\nFAIL\texample.org/tests\t0.123s\nFAIL\n"
	got := commandCompactAssertChanged(t, "go test -v ./...", output, "go-test")
	commandCompactAssertKept(t, got, "=== RUN   TestÉchec_日本\n", diagnostic, "--- FAIL: TestÉchec_日本 (0.01s)\n", "=== RUN   TestPassingWithLog\n", "warning: external service unavailable\n", "--- PASS: TestPassingWithLog (0.00s)\n", "FAIL\texample.org/tests\t0.123s\n")
	if strings.Contains(got, "TestQuiet") {
		t.Fatal("quiet successes around a failure were not removed")
	}
}

func TestCompactCommandOutputGoParallelUnknownKeepsOpenContexts(t *testing.T) {
	parallel := "=== RUN   TestFirst\n=== PAUSE TestFirst\n=== RUN   TestSecond\n=== PAUSE TestSecond\n=== CONT  TestFirst\nstdout with no reliable source /workspace/results.txt\n--- PASS: TestFirst (0.00s)\n=== CONT  TestSecond\n--- PASS: TestSecond (0.00s)\n"
	output := commandCompactGoSuccesses(24) + parallel + "PASS\nok  \texample.org/parallel\t0.123s\n"
	got := commandCompactAssertChanged(t, "go test -v -parallel 2 ./...", output, "go-test")
	commandCompactAssertKept(t, got, parallel)
}

func TestCompactCommandOutputGoNestedFailureKeepsAncestors(t *testing.T) {
	output := commandCompactGoSuccesses(24) +
		"=== RUN   TestParent\n=== RUN   TestParent/quiet\n=== RUN   TestParent/failure\n" +
		"    nested_test.go:8: incorrect result\n--- FAIL: TestParent (0.00s)\n" +
		"    --- PASS: TestParent/quiet (0.00s)\n    --- FAIL: TestParent/failure (0.00s)\nFAIL\nFAIL\texample.org/nested\t0.123s\n"
	got := commandCompactAssertChanged(t, "go test -v ./...", output, "go-test")
	commandCompactAssertKept(t, got, "=== RUN   TestParent\n", "=== RUN   TestParent/failure\n", "nested_test.go:8: incorrect result\n", "--- FAIL: TestParent (0.00s)\n", "--- FAIL: TestParent/failure (0.00s)\n")
	// Go delays child result lines until the parent ends. The file/line log
	// belongs to the current sibling, not to the already-finished quiet child.
	if strings.Contains(got, "TestParent/quiet") {
		t.Fatal("an earlier quiet sibling was mistaken for the current test")
	}
}

func TestCompactCommandOutputTAPQuietAndNestedSuccess(t *testing.T) {
	output := "TAP version 13\n" + commandCompactTAPSuccesses(24) +
		"# Subtest: parent été\n    # Subtest: 日本\n    ok 1 - 日本\n      ---\n      duration_ms: 0.5\n      type: 'test'\n      ...\n    1..1\nok 25 - parent été\n  ---\n  duration_ms: 1\n  type: 'suite'\n  ...\n" +
		commandCompactTAPSummary(25, 26, 26, 0)
	got := commandCompactAssertChanged(t, "node --test --test-reporter=tap test/example.test.js", output, "tap")
	commandCompactAssertKept(t, got, "26 passing result lines", "26 subtest headers", "104 timing/type metadata lines", "1 nested plans", "TAP version 13\n", "# pass 26\n", "# fail 0\n")
	if strings.Contains(got, "quiet case") || strings.Contains(got, "parent été") || strings.Contains(got, "日本") {
		t.Fatal("quiet nested successes were not replaced by counters")
	}
}

func TestCompactCommandOutputTAPNestedFailure(t *testing.T) {
	failure := "    # Subtest: échec 日本\n    not ok 2 - échec 日本\n      ---\n      duration_ms: 0.5\n      location: '/workspace/tests/été.test.js:42:1'\n      error: 'expected 42; got 41'\n      expected: 42\n      actual: 41\n      stack: |-\n        at /workspace/tests/été.test.js:42:7\n      ...\n"
	output := "TAP version 13\n" + commandCompactTAPSuccesses(24) +
		"# Subtest: parent\n    # Subtest: quiet child\n    ok 1 - quiet child\n      ---\n      duration_ms: 0.1\n      type: 'test'\n      ...\n" + failure +
		"    1..2\nnot ok 25 - parent\n  ---\n  duration_ms: 1\n  error: '1 subtest failed'\n  ...\n" + commandCompactTAPSummary(25, 27, 25, 2)
	got := commandCompactAssertChanged(t, "node --test test/nested.test.js", output, "tap")
	commandCompactAssertKept(t, got, "# Subtest: parent\n", failure, "    1..2\n", "not ok 25 - parent\n", "error: '1 subtest failed'\n", "# fail 2\n")
	if strings.Contains(got, "quiet child") || strings.Contains(got, "quiet case") {
		t.Fatal("quiet successful siblings were retained")
	}
}

func TestCompactCommandOutputTAPDiagnosticAfterChildResult(t *testing.T) {
	child := func(id int, name string) string {
		return fmt.Sprintf("    # Subtest: %s\n    ok %d - %s\n      ---\n      duration_ms: 0.1\n      type: 'test'\n      ...\n", name, id, name)
	}
	logged := child(2, "logged middle 日本")
	next := child(3, "quiet last")
	output := "TAP version 13\n" + commandCompactTAPSuccesses(24) + "# Subtest: suite\n" + child(1, "quiet first") + logged +
		"    # CENTRAL_NESTED_DIAGNOSTIC /workspace/données.json\n" + next +
		"    1..3\nok 25 - suite\n  ---\n  duration_ms: 0.5\n  type: 'suite'\n  ...\n" + commandCompactTAPSummary(25, 27, 27, 0)
	got := commandCompactAssertChanged(t, "node --test", output, "tap")
	commandCompactAssertKept(t, got, "# Subtest: suite\n", logged, "    # CENTRAL_NESTED_DIAGNOSTIC /workspace/données.json\n", next, "    1..3\n", "ok 25 - suite\n")
	if strings.Contains(got, "quiet first") || strings.Contains(got, "quiet case") {
		t.Fatal("unrelated quiet successes were retained")
	}
}

func TestCompactCommandOutputTAPUnknownMetadataLogsAndPaths(t *testing.T) {
	withMetadata := "# Subtest: path metadata\nok 25 - path metadata\n  ---\n  duration_ms: 0.1\n  location: '/workspace/data/é.json:3:1'\n  custom: 'diagnostic to retain'\n  ...\n"
	withLog := "# Subtest: logged success\n# warning: input /workspace/données.csv is empty\nok 26 - logged success\n  ---\n  duration_ms: 0.2\n  ...\n"
	fileCase := "# Subtest: /workspace/test/default.test.js\nok 27 - /workspace/test/default.test.js\n  ---\n  duration_ms: 0.2\n  ...\n"
	output := "TAP version 13\n" + commandCompactTAPSuccesses(24) + "(node:123) ExperimentalWarning: sample warning\n" + withMetadata + withLog + fileCase + commandCompactTAPSummary(27, 27, 27, 0)
	got := commandCompactAssertChanged(t, "node --test", output, "tap")
	commandCompactAssertKept(t, got, "(node:123) ExperimentalWarning: sample warning\n", withMetadata, withLog, fileCase)
}

func TestCompactCommandOutputTAPSkipsAndTodos(t *testing.T) {
	skip := "# Subtest: skipped case\nok 25 - skipped case # SKIP requires database\n  ---\n  duration_ms: 0.2\n  ...\n"
	todo := "# Subtest: pending case\nnot ok 26 - pending case # TODO not implemented\n  ---\n  duration_ms: 0.2\n  error: 'missing implementation'\n  ...\n"
	output := "TAP version 13\n" + commandCompactTAPSuccesses(24) + skip + todo + commandCompactTAPSummary(26, 26, 24, 0)
	got := commandCompactAssertChanged(t, "node --test", output, "tap")
	commandCompactAssertKept(t, got, skip, todo)
}

func TestCompactCommandOutputUnattributedLogsKeepAdjacentContexts(t *testing.T) {
	goAdjacent := "=== RUN   TestAdjacent\n--- PASS: TestAdjacent (0.00s)\n"
	goOutput := commandCompactGoSuccesses(24) + "unattributed stdout /workspace/results.json\n" + goAdjacent + "PASS\nok  \texample.org/tests\t0.123s\n"
	goGot := commandCompactAssertChanged(t, "go test -v", goOutput, "go-test")
	commandCompactAssertKept(t, goGot, "=== RUN   TestQuiet023\n--- PASS: TestQuiet023 (0.00s)\n", "unattributed stdout /workspace/results.json\n", goAdjacent)
	if strings.Contains(goGot, "TestQuiet022") {
		t.Fatal("unrelated quiet context was retained")
	}
	nested := "# Subtest: adjacent suite\n    # Subtest: child\n    ok 1 - child\n      ---\n      duration_ms: 0.1\n      type: 'test'\n      ...\n    1..1\nok 25 - adjacent suite\n  ---\n  duration_ms: 0.2\n  type: 'suite'\n  ...\n"
	tapOutput := "TAP version 13\n" + commandCompactTAPSuccesses(24) + "# unattributed stdout /workspace/results.json\n" + nested + commandCompactTAPSummary(25, 26, 26, 0)
	tapGot := commandCompactAssertChanged(t, "node --test", tapOutput, "tap")
	commandCompactAssertKept(t, tapGot, "# Subtest: quiet case 024\n", "ok 24 - quiet case 024\n", "# unattributed stdout /workspace/results.json\n", nested)
	if strings.Contains(tapGot, "quiet case 023") {
		t.Fatal("unrelated quiet context was retained")
	}
}

func TestCompactCommandOutputCRLFPreservesDiagnostics(t *testing.T) {
	diagnostic := "    case_test.go:42: diagnostic 日本 /workspace/é.json\r\n"
	output := strings.ReplaceAll(commandCompactGoSuccesses(24), "\n", "\r\n") + "=== RUN   TestDiagnostic\r\n" + diagnostic + "--- FAIL: TestDiagnostic (0.00s)\r\nFAIL\r\nFAIL\texample.org/tests\t0.123s\r\n"
	got := commandCompactAssertChanged(t, "go test -v", output, "go-test")
	commandCompactAssertKept(t, got, diagnostic, "--- FAIL: TestDiagnostic (0.00s)\r\n", "FAIL\texample.org/tests\t0.123s\r\n")
}

func TestCompactCommandOutputCommandBoundary(t *testing.T) {
	goOutput := commandCompactGoSuccesses(24) + "PASS\nok  \texample.org/tests\t0.123s\n"
	tapOutput := "TAP version 13\n" + commandCompactTAPSuccesses(24) + commandCompactTAPSummary(24, 24, 24, 0)
	cases := []struct{ command, output string }{
		{"cat tests.log", goOutput}, {"cat source.go", goOutput}, {"printf '%s' data", goOutput},
		{"echo go test -v", goOutput}, {"python test.py", goOutput}, {"go run helper.go", goOutput},
		{"node file.js --test", tapOutput}, {"node -e 'console.log(1)' --test", tapOutput}, {"npm test", tapOutput},
		{"go test -v | cat", goOutput}, {"go test -v > results.txt", goOutput},
		{"go test -v; cat data", goOutput}, {"go test -v && cat data", goOutput}, {"go test -v &", goOutput},
		{"go test -v $(cat args)", goOutput}, {"go test -v `cat args`", goOutput}, {"go test -v $ARGS", goOutput},
		{"go test -v <<EOF\nEOF", goOutput}, {"go test -v\ncat data", goOutput}, {"go test -v # comment", goOutput},
		{"ENV=value go test -v", goOutput}, {"cd path && go test -v && pwd", goOutput}, {"cd path; go test -v", goOutput},
		{"cd -- path && go test -v", goOutput}, {"go test -v './unterminated", goOutput}, {"go test -v ./pkg/*", goOutput},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			got, filter := compactCommandOutput(tc.command, tc.output)
			if got != tc.output || filter != "" {
				t.Fatalf("ambiguous or non-test command changed output: filter=%q", filter)
			}
		})
	}
	commandCompactAssertChanged(t, `cd '/workspace/my project' && go test -v -run 'TestQuiet' ./...`, goOutput, "go-test")
	commandCompactAssertChanged(t, `cd "/workspace/my project" && node --test --test-name-pattern 'quiet case' test/example.test.js`, tapOutput, "tap")
}

func TestCompactCommandOutputIncompatibleFlags(t *testing.T) {
	goOutput := commandCompactGoSuccesses(24) + "PASS\nok  \texample.org/tests\t0.123s\n"
	tapOutput := "TAP version 13\n" + commandCompactTAPSuccesses(24) + commandCompactTAPSummary(24, 24, 24, 0)
	cases := []struct{ command, output string }{
		{"go test -json", goOutput}, {"go test -json=false -v", goOutput}, {"go test -bench=.", goOutput},
		{"go test -benchmem -v", goOutput}, {"go test -fuzz FuzzParse", goOutput}, {"go test -list .", goOutput},
		{"go test -c", goOutput}, {"go test -o custom.test", goOutput}, {"go test -exec helper", goOutput},
		{"go test -v -args -custom", goOutput}, {"go test -unknown", goOutput}, {"go test -run", goOutput},
		{"node --test --test-reporter=json", tapOutput}, {"node --test --test-reporter spec", tapOutput},
		{"node --test --test-reporter=tap --test-reporter=spec", tapOutput}, {"node --test --require bootstrap.js", tapOutput},
		{"node --test --import=bootstrap.js", tapOutput}, {"node --test --test-name-pattern", tapOutput},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			got, filter := compactCommandOutput(tc.command, tc.output)
			if got != tc.output || filter != "" {
				t.Fatalf("incompatible flags changed output: filter=%q", filter)
			}
		})
	}
}

func TestCompactCommandOutputOpaqueAndIncomplete(t *testing.T) {
	goOutput := commandCompactGoSuccesses(24) + "PASS\nok  \texample.org/tests\t0.123s\n"
	tapOutput := "TAP version 13\n" + commandCompactTAPSuccesses(24) + commandCompactTAPSummary(24, 24, 24, 0)
	cases := []struct{ command, output string }{
		{"go test -v", `{"message":"=== RUN   TestData","result":"PASS"}`},
		{"go test -v", "[\n" + goOutput + "]\n"},
		{"go test -v", "opaque application output\n"},
		{"go test -v", commandCompactGoSuccesses(24)},
		{"go test -v", "unattributed TestMain stdout\n" + goOutput},
		{"go test -v", "package example\nconst transcript = `\n" + goOutput + "`\n"},
		{"go test -v", goOutput + "\x00binary"},
		{"go test -v", goOutput + "\xff"},
		{"go test -v", goOutput + "\x1b[31mcolored\x1b[0m"},
		{"go test -v", goOutput + "progress\roverwritten\n"},
		{"node --test", strings.Replace(tapOutput, "1..24\n", "", 1)},
		{"node --test", strings.Replace(tapOutput, "# fail 0\n", "", 1)},
		{"node --test", strings.Replace(tapOutput, "1..24\n", "1..25\n", 1)},
		{"node --test", strings.Replace(tapOutput, "ok 1 - quiet case 001", "ok 1 - different name", 1)},
		{"node --test", strings.Replace(tapOutput, "  ...\n", "", 1)},
		{"node --test", "TAP version 13\n# Subtest: unfinished\n"},
		{"node --test", strings.Replace(tapOutput, "TAP version 13", "TAP version 14", 1)},
		{"node --test", "unattributed console.log from a middle test\n" + tapOutput},
		{"node --test", "(node:123) ExperimentalWarning: sample warning\n" + tapOutput},
		{"node --test", strings.Replace(tapOutput, "TAP version 13\n", "TAP version 13\n# unattributed console.log before any subtest\n", 1)},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, filter := compactCommandOutput(tc.command, tc.output)
			if got != tc.output || filter != "" {
				t.Fatalf("opaque or incomplete output changed: filter=%q", filter)
			}
		})
	}
}

func TestCompactCommandOutputLargeDiagnosticsRemainExact(t *testing.T) {
	diagnostic := strings.Repeat("    case_test.go:42: diagnostic 日本 /workspace/data/é.json expected=42 actual=41\n", 400)
	goOutput := commandCompactGoSuccesses(24) + "=== RUN   TestLargeDiagnostic\n" + diagnostic + "--- FAIL: TestLargeDiagnostic (0.01s)\nFAIL\nFAIL\texample.org/tests\t0.123s\n"
	goGot := commandCompactAssertChanged(t, "go test -v ./...", goOutput, "go-test")
	commandCompactAssertKept(t, goGot, diagnostic)
	if strings.Count(goGot, "case_test.go:42:") != 400 {
		t.Fatal("Go diagnostic lines were changed or omitted")
	}
	tapDiagnostic := strings.Repeat("    at /workspace/tests/été.test.js:42:7; diagnostic 日本\n", 400)
	tapOutput := "TAP version 13\n" + commandCompactTAPSuccesses(24) + "# Subtest: failure\nnot ok 25 - failure\n  ---\n  duration_ms: 0.1\n  error: 'large diagnostic'\n  stack: |-\n" + tapDiagnostic + "  ...\n" + commandCompactTAPSummary(25, 25, 24, 1)
	tapGot := commandCompactAssertChanged(t, "node --test", tapOutput, "tap")
	commandCompactAssertKept(t, tapGot, tapDiagnostic)
	if strings.Count(tapGot, "diagnostic 日本") != 400 {
		t.Fatal("TAP diagnostic lines were changed or omitted")
	}
}

func TestCompactCommandOutputIdempotentAndNoGain(t *testing.T) {
	output := commandCompactGoSuccesses(24) + "PASS\nok  \texample.org/tests\t0.123s"
	got := commandCompactAssertChanged(t, "go test -v ./...", output, "go-test")
	if strings.HasSuffix(got, "\n") {
		t.Fatal("changed the final retained line's missing newline")
	}
	again, filter := compactCommandOutput("go test -v ./...", got)
	if again != got || filter != "" {
		t.Fatal("already compact output changed")
	}
	// A long marker is not useful for a very short transcript.
	small := "=== RUN   TestA\n--- PASS: TestA (0.00s)\nPASS\nok  \tp\t0.001s\n"
	unchanged, filter := compactCommandOutput("go test -v", small)
	if unchanged != small || filter != "" {
		t.Fatal("returned a filter without a byte reduction")
	}
}
