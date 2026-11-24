package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCommandCompactRunnerShellWrappers(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"go test -v ./...", []string{"go", "test", "-v", "./..."}},
		{"go test -v ./... 2>&1", []string{"go", "test", "-v", "./..."}},
		{`go test -v ./... 2>&1; echo "EXIT:$?"`, []string{"go", "test", "-v", "./..."}},
		{`go test -v ./...; echo exit=$?`, []string{"go", "test", "-v", "./..."}},
		{`go test -v ./... && echo "STATUS:$?"`, []string{"go", "test", "-v", "./..."}},
		{`cd /workspace && go test -v ./... 2>&1 | tee /tmp/gotest.log; echo "exit=$?"`, []string{"cd", "/workspace", "&&", "go", "test", "-v", "./..."}},
		{`cd "/workspace/project with spaces" && go test -v -run 'TestInvoice/case-018' ./... 2>&1 | tee -a "logs/test output.log"`, []string{"cd", "/workspace/project with spaces", "&&", "go", "test", "-v", "-run", "TestInvoice/case-018", "./..."}},
		{`go test -v ./...|tee --append -- /tmp/go.log logs/second.log`, []string{"go", "test", "-v", "./..."}},
		{`node --test --test-reporter=tap shipping.test.mjs 2>&1`, []string{"node", "--test", "--test-reporter=tap", "shipping.test.mjs"}},
		{`node --test --test-reporter=tap shipping.test.mjs 2>&1; echo "EXIT:$?"`, []string{"node", "--test", "--test-reporter=tap", "shipping.test.mjs"}},
		{"\tgo\ttest -v ./...\t2>&1\t|\ttee /tmp/run.log\t;\techo \"EXIT:$?\"\t", []string{"go", "test", "-v", "./..."}},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			got, ok := commandCompactRunnerWords(tc.command)
			if !ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
}

func TestCommandCompactRunnerShellRejectsNonIdentityAndAmbiguity(t *testing.T) {
	commands := []string{
		"", " ", "go test -v ./...\n", "go test -v ./...\r", "go test -v ./...\x00", "go test -v ./...\x7f", "go test -v ./...\xff",
		"go test -json ./... 2>&1", "go test -bench=. 2>&1", "node --test --test-reporter=json 2>&1", "node audit-stock.mjs 2>&1", "cat runner.txt | tee /tmp/log",
		"go test -v ./... | grep PASS", "go test -v ./... | head -20", "go test -v ./... | tail -20", "go test -v ./... | python3 summarize.py",
		"go test -v ./... | tee /tmp/log | grep PASS", "go test -v ./... | tee /tmp/log | tee /tmp/other", "go test -v ./... || true", "go test -v ./... &",
		"go version && go test -v ./...", "ls && go test -v ./...", "cd /workspace && ls && go test -v ./...", "cd /workspace && cd /tmp && go test -v ./...", "go test -v ./...; cat /tmp/data",
		"go test -v ./... > /tmp/log", "go test -v ./... 2>/tmp/err", "go test -v ./... 1>&1", "go test -v ./... 2 >&1", "go test -v ./...2>&1", "go test -v ./... 2>&10", "go test -v ./... 2>&1file", "go test -v ./... 2>&1 2>&1", "go test 2>&1 -v ./...",
		"go test -v ./... |& tee /tmp/log", "go test -v ./... | tee /tmp/log 2>&1", "go test -v ./... 2>&1 | tee /tmp/log > /tmp/other",
		`go test -v ./...; echo 'EXIT:$?'`, `go test -v ./...; echo "EXIT:${PIPESTATUS[0]}"`, `go test -v ./...; echo "EXIT:$(id)"`, `go test -v ./...; echo "EXIT:$?"; echo "EXIT:$?"`, `go test -v ./...; echo "EXIT:$?" trailing`, `go test -v ./...; echo -n "EXIT:$?"`, `go test -v ./...; /bin/echo "EXIT:$?"`, `go test -v ./...; echo"EXIT:$?"`, `go test -v ./...; echo 'EXIT:'"$?"`,
		`go test -run "literal 2>&1" ./...`, `go test -run 'literal | tee /tmp/log' ./...`, `go test -run 'literal; echo "EXIT:$?"' ./...`, `go test -run 'foo && echo "EXIT:$?"' ./...`,
		`go test -run "$(printf Test)" ./... 2>&1`, `go test -run "$TEST_NAME" ./... 2>&1`, "GOTMPDIR=/tmp go test -v ./... 2>&1", "env go test -v ./... 2>&1", "go test -v ./... # a comment",
		"go test -v ./... | tee -", "go test -v ./... | tee /dev/stdout", "go test -v ./... | tee /dev/fd/1", "go test -v ./... | tee /proc/self/fd/1", "go test -v ./... | tee /tmp/../dev/stdout", "go test -v ./... | tee ../../proc/self/fd/1", "go test -v ./... | tee /sys/kernel/debug/tracing/trace_marker",
		"go test -v ./... | tee --output-error=warn /tmp/log", "go test -v ./... | tee -p /tmp/log", "go test -v ./... | tee -- /dev/stdout", "go test -v ./... | tee", `go test -v ./... | tee ""`, "go test -v ./... | tee $LOG_FILE", "go test -v ./... | tee *.log", "go test -v ./... | tee /tmp/a\\ b.log",
		`go test -v ./... | tee "file; echo EXIT:$?"`, `go test -v ./... | tee "file | tee /dev/stdout"`, `go test -v ./... | tee "file 2>&1"`, `go test -v ./... | tee "file && echo EXIT:$?"`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			if words, ok := commandCompactRunnerWords(command); ok {
				t.Fatalf("ambiguous or unsupported command accepted as %q", words)
			}
		})
	}
}

func TestCommandCompactShellKeepsStatusFooters(t *testing.T) {
	goOutput := commandCompactGoSuccesses(40) + "PASS\nok  \texample.org/tests\t0.123s\nEXIT:0\n"
	words, ok := commandCompactRunnerWords(`go test -v ./... 2>&1; echo "EXIT:$?"`)
	if !ok || !commandCompactGoCommand(words) {
		t.Fatal("Go wrapper not recognized")
	}
	goResult := commandCompactGo(goOutput)
	if len(goResult) >= len(goOutput) || !strings.HasSuffix(goResult, "EXIT:0\n") {
		t.Fatal("Go status footer was lost or prevented compaction")
	}
	tapOutput := "TAP version 13\n" + commandCompactTAPSuccesses(40) + commandCompactTAPSummary(40, 40, 40, 0) + "exit=0\n"
	words, ok = commandCompactRunnerWords(`node --test --test-reporter=tap 2>&1 | tee /tmp/tap.log; echo "exit=$?"`)
	if !ok || !commandCompactNodeCommand(words) {
		t.Fatal("Node wrapper not recognized")
	}
	tapResult := commandCompactTAP(tapOutput)
	if len(tapResult) >= len(tapOutput) || !strings.HasSuffix(tapResult, "exit=0\n") {
		t.Fatal("TAP status footer was lost or prevented compaction")
	}
	commandCompactAssertKept(t, tapResult, "# tests 40\n", "# pass 40\n", "# fail 0\n")
}

func TestCommandCompactLiteralWordsRemainStrict(t *testing.T) {
	for _, command := range []string{`go test -v ./... 2>&1`, `go test -v ./... | tee /tmp/run.log`, `go test -v ./...; echo "EXIT:$?"`} {
		if _, ok := commandCompactWords(command); ok {
			t.Fatalf("literal parser unexpectedly broadened: %s", command)
		}
	}
}

func TestCommandCompactRunnerFooterExactBytes(t *testing.T) {
	cases := []struct {
		command, body, footer string
	}{
		{`go test -v ./... 2>&1; echo "EXIT:$?"`, "runner stdout\n", "EXIT:0\n"},
		{`cd /workspace && go test -v ./...; echo status=$?`, "runner stdout\r\n", "status=1\r\n"},
		{`node --test --test-reporter=tap shipping.test.mjs 2>&1 | tee /tmp/tap.log; echo "exit=$?"`, "runner stdout\n", "exit=127\n"},
		{`go test -v ./...; echo RC:$?`, "runner stdout\nEXIT:0\n", "RC:255\n"},
		{`go test -v ./...; echo "EXIT:$?"`, "runner stdout\nEXIT:0\n", "EXIT:0\n"},
	}
	for _, tc := range cases {
		t.Run(tc.footer+tc.command, func(t *testing.T) {
			output := tc.body + tc.footer
			body, footer := commandCompactRunnerFooter(tc.command, output)
			if body != tc.body || footer != tc.footer || body+footer != output {
				t.Fatalf("split changed bytes: body=%q footer=%q", body, footer)
			}
		})
	}
}

func TestCommandCompactRunnerFooterRejectsLookalikesAndNonterminalLines(t *testing.T) {
	const command = `node --test --test-reporter=tap shipping.test.mjs 2>&1; echo "EXIT:$?"`
	outputs := []string{
		"", "TAP version 13\n", "TAP version 13\nEXIT:0", "TAP version 13\nEXIT:0\r",
		"TAP version 13\nEXIT:0\nmore output\n", "TAP version 13\nEXIT:0\n\n",
		"TAP version 13\nexit:0\n", "TAP version 13\nEXIT=0\n", "TAP version 13\n EXIT:0\n",
		"TAP version 13\nEXIT:0 \n", "TAP version 13\nEXIT: 0\n", "TAP version 13\nEXIT:-1\n",
		"TAP version 13\nEXIT:+0\n", "TAP version 13\nEXIT:00\n", "TAP version 13\nEXIT:001\n",
		"TAP version 13\nEXIT:256\n", "TAP version 13\nEXIT:9999\n", "TAP version 13\nEXIT:one\n",
		"TAP version 13\nEXIT:0\x00\n", "TAP version 13\nEXIT:0 (actual data)\n",
	}
	for _, output := range outputs {
		if body, footer := commandCompactRunnerFooter(command, output); body != output || footer != "" {
			t.Errorf("lookalike or nonterminal footer was separated: %q", output)
		}
	}
	commands := []string{
		"node --test --test-reporter=tap shipping.test.mjs 2>&1",
		`node --test --test-reporter=tap shipping.test.mjs 2>&1 && echo "EXIT:$?"`,
		`node --test --test-reporter=tap shipping.test.mjs; echo 'EXIT:$?'`,
		`node --test --test-reporter=tap shipping.test.mjs; echo "OTHER:$?"`,
		`node --test --test-reporter=tap shipping.test.mjs; echo "EXIT:$?"; cat data`,
		`node --test --test-reporter=tap shipping.test.mjs | grep PASS; echo "EXIT:$?"`,
		`node audit-stock.mjs; echo "EXIT:$?"`,
		`go test -json ./...; echo "EXIT:$?"`,
		`cat test.log; echo "EXIT:$?"`,
		`go version && go test -v ./...; echo "EXIT:$?"`,
		`go test -run 'literal; echo "EXIT:$?"' ./...`,
	}
	const output = "TAP version 13\nEXIT:0\n"
	for _, candidate := range commands {
		if body, footer := commandCompactRunnerFooter(candidate, output); body != output || footer != "" {
			t.Errorf("unsupported or conditional command separated data as a shell footer: %s", candidate)
		}
	}
}

func TestCommandCompactRunnerFooterPreservesNestedTAPCounts(t *testing.T) {
	const children = 180
	var b strings.Builder
	b.WriteString("TAP version 13\n# Subtest: shipment eligibility\n")
	for i := 1; i <= children; i++ {
		fmt.Fprintf(&b, "    # Subtest: shipment %03d\n    ok %d - shipment %03d\n      ---\n      duration_ms: 0.125\n      type: 'test'\n      ...\n", i, i, i)
	}
	fmt.Fprintf(&b, "    1..%d\nok 1 - shipment eligibility\n  ---\n  duration_ms: 1.25\n  type: 'test'\n  ...\n", children)
	b.WriteString(commandCompactTAPSummary(1, children+1, children+1, 0))
	const footer = "EXIT:0\n"
	output := b.String() + footer
	if len(output) < 8000 {
		t.Fatal("fixture must exceed the existing preview")
	}
	body, gotFooter := commandCompactRunnerFooter(`node --test --test-reporter=tap shipping.test.mjs 2>&1; echo "EXIT:$?"`, output)
	compacted := commandCompactTAP(body) + gotFooter
	if gotFooter != footer || len(compacted) >= 1000 || !strings.HasSuffix(compacted, footer) {
		t.Fatalf("status footer retained the whole nested TAP tree: raw=%d final=%d footer=%q", len(output), len(compacted), gotFooter)
	}
	commandCompactAssertKept(t, compacted, "# tests 181\n", "# pass 181\n", "# fail 0\n")
	if strings.Contains(compacted, "shipment 001") || strings.Contains(compacted, "shipment 180") {
		t.Fatal("quiet child contexts unnecessarily retained by the shell status footer")
	}
}
