package agent

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This corpus contains no account data and needs no external process, model,
// database, or Docker. Only prism-go-public-pass.txt is captured output; the
// remaining cases are deliberately synthetic, not recorded production traces.
// The captured fixture was generated with:
// go test -v ./internal/cronclock ./internal/timeprefs
// Those packages exercise local time parsing and cron string wrapping only.
//
//go:embed testdata/command_output/prism-go-public-pass.txt
var commandOutputPublicPrismGoPass string

type commandOutputCorpusCase struct {
	name              string
	origin            string
	command           string
	output            string
	requireCompaction bool
	unchanged         bool
	mustKeep          []string
	centralDiagnostic string
}

func commandOutputCorpus() []commandOutputCorpusCase {
	goPass := commandOutputSyntheticGo(false)
	goFailure := commandOutputSyntheticGo(true)
	tapPass := commandOutputSyntheticTAP(false)
	tapFailure := commandOutputSyntheticTAP(true)
	jsonOutput := "[\n" + strings.Repeat("  {\"message\":\"=== RUN   TestData\",\"result\":\"--- PASS: TestData (0.00s)\"},\n", 160) + "  {\"result\":\"FAIL\",\"payload\":\"preserve JSON exactly\"}\n]\n"
	unknown := strings.Repeat("measurement: baseline=42 observed=43 detail=keep-all-lines\n", 280)
	code := "package example\n\nconst testTranscript = `\n" + goPass + "`\n"
	return []commandOutputCorpusCase{
		{
			name: "real_prism_go_pass", origin: "captured public Prism unit-test output",
			command: "go test -v ./internal/cronclock ./internal/timeprefs", output: commandOutputPublicPrismGoPass,
			mustKeep: []string{"prism/internal/cronclock", "prism/internal/timeprefs"},
		},
		{
			name: "synthetic_go_240_pass", origin: "synthetic verbose Go success transcript",
			command: "go test -v ./internal/example", output: goPass, requireCompaction: true,
			mustKeep: []string{"PASS", "prism/internal/example"},
		},
		{
			name: "synthetic_go_central_failure", origin: "synthetic verbose Go transcript with a failure in its middle",
			command: "go test -v ./internal/example", output: goFailure, requireCompaction: true,
			mustKeep:          []string{"TestSynthetic120", "example_test.go:121: CENTRAL_GO_FAILURE: expected 42; got 41", "--- FAIL:", "FAIL\tprism/internal/example"},
			centralDiagnostic: "CENTRAL_GO_FAILURE",
		},
		{
			name: "synthetic_node_tap_160_pass", origin: "synthetic Node 22 TAP success transcript",
			command: "node --test test/example.test.js", output: tapPass, requireCompaction: true,
			mustKeep: []string{"# tests 160", "# pass 160", "# fail 0"},
		},
		{
			name: "synthetic_node_tap_central_failure", origin: "synthetic Node 22 TAP transcript with a failure in its middle",
			command: "node --test test/example.test.js", output: tapFailure, requireCompaction: true,
			mustKeep:          []string{"not ok 80 - synthetic case 080", "CENTRAL_TAP_FAILURE", "expected: 42", "actual: 41", "example.test.js:80:12", "# fail 1"},
			centralDiagnostic: "CENTRAL_TAP_FAILURE",
		},
		{
			name: "synthetic_unknown_plaintext", origin: "synthetic unrecognized command/output",
			command: "python report.py", output: unknown, unchanged: true,
		},
		{
			name: "synthetic_unknown_from_go_test", origin: "synthetic unrecognized text emitted by a supported command",
			command: "go test -v ./...", output: unknown, unchanged: true,
		},
		{
			name: "synthetic_json_from_cat", origin: "synthetic JSON containing test-like text",
			command: "cat results.json", output: jsonOutput, unchanged: true,
		},
		{
			name: "synthetic_json_from_go_test", origin: "synthetic structured output must remain byte-for-byte unchanged",
			command: "go test -json ./...", output: jsonOutput, unchanged: true,
		},
		{
			name: "synthetic_source_code", origin: "synthetic source code containing a test transcript literal",
			command: "cat fixture.go", output: code, unchanged: true,
		},
		{
			name: "synthetic_command_mismatch", origin: "synthetic test transcript printed by an unrecognized command",
			command: "cat go-test.log", output: goPass, unchanged: true,
		},
	}
}

func commandOutputSyntheticGo(fail bool) string {
	var out strings.Builder
	for i := 1; i <= 240; i++ {
		fmt.Fprintf(&out, "=== RUN   TestSynthetic%03d\n", i)
		if fail && i == 120 {
			out.WriteString("    example_test.go:121: CENTRAL_GO_FAILURE: expected 42; got 41\n")
			out.WriteString("    example_test.go:122: input case: empty title\n")
			fmt.Fprintf(&out, "--- FAIL: TestSynthetic%03d (0.00s)\n", i)
		} else {
			fmt.Fprintf(&out, "--- PASS: TestSynthetic%03d (0.00s)\n", i)
		}
	}
	if fail {
		out.WriteString("FAIL\nFAIL\tprism/internal/example\t0.123s\nFAIL\n")
	} else {
		out.WriteString("PASS\nok  \tprism/internal/example\t0.123s\n")
	}
	return out.String()
}

func commandOutputSyntheticTAP(fail bool) string {
	var out strings.Builder
	out.WriteString("TAP version 13\n")
	for i := 1; i <= 160; i++ {
		fmt.Fprintf(&out, "# Subtest: synthetic case %03d\n", i)
		if fail && i == 80 {
			fmt.Fprintf(&out, "not ok %d - synthetic case %03d\n", i, i)
			out.WriteString("  ---\n  duration_ms: 0.25\n  location: '/workspace/test/example.test.js:80:1'\n  failureType: 'testCodeFailure'\n  error: 'CENTRAL_TAP_FAILURE: values differ'\n  code: 'ERR_ASSERTION'\n  name: 'AssertionError'\n  expected: 42\n  actual: 41\n  operator: 'strictEqual'\n  stack: |-\n    TestContext.<anonymous> (/workspace/test/example.test.js:80:12)\n  ...\n")
		} else {
			fmt.Fprintf(&out, "ok %d - synthetic case %03d\n  ---\n  duration_ms: 0.125\n  type: 'test'\n  ...\n", i, i)
		}
	}
	failures := 0
	if fail {
		failures = 1
	}
	fmt.Fprintf(&out, "1..160\n# tests 160\n# suites 0\n# pass %d\n# fail %d\n# cancelled 0\n# skipped 0\n# todo 0\n# duration_ms 20.5\n", 160-failures, failures)
	return out.String()
}

// This mirrors the pre-prototype exec_command response. The marker adds 19
// bytes, so the actual current response ceiling is 8,019 bytes, not 8,000.
func commandOutputCurrentView(output string) string {
	if len(output) > 8000 {
		return output[:4000] + "\n...[truncated]...\n" + output[len(output)-4000:]
	}
	return output
}

// This is an explicit estimate for the recovery-file notice, not a measured
// tool response. Apply the current response limit to both alternatives and
// charge this overhead only when the filter changes output. The prototype's
// benefit gate must then accept the candidate (20% and at least 256 bytes).
// No token or monetary estimate is inferred from bytes. All sizes concern
// stdout only; error text, archive bytes, and persistence costs are excluded.
const commandOutputRecoveryHintEstimateBytes = 180

func commandOutputProposedViewBytes(raw, compact string) int {
	current := len(commandOutputCurrentView(raw))
	if compact == raw {
		return current
	}
	size := len(commandOutputCurrentView(compact))
	size += commandOutputRecoveryHintEstimateBytes
	saved := current - size
	if saved < 256 || saved*5 < current {
		return current
	}
	return size
}

func TestCommandOutputCorpusQuality(t *testing.T) {
	for _, tc := range commandOutputCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			got, filter := compactCommandOutput(tc.command, tc.output)
			if (filter != "") != (got != tc.output) {
				t.Fatal("filter label does not match whether output changed")
			}
			if tc.unchanged && got != tc.output {
				t.Fatal("filter changed unrecognized or structured output")
			}
			if tc.requireCompaction {
				current := len(commandOutputCurrentView(tc.output))
				proposed := commandOutputProposedViewBytes(tc.output, got)
				if proposed >= current {
					t.Errorf("no reduction against current response after charging %d-byte recovery hint: current=%d proposed=%d", commandOutputRecoveryHintEstimateBytes, current, proposed)
				}
			}
			// Check the limited stdout view, not only the intermediate compact
			// string: a diagnostic can otherwise be lost to the final limit.
			// The estimated recovery notice is charged in the byte comparison;
			// it is not injected into this diagnostic-preservation check.
			modelView := commandOutputCurrentView(got)
			if commandOutputProposedViewBytes(tc.output, got) == len(commandOutputCurrentView(tc.output)) {
				modelView = commandOutputCurrentView(tc.output)
			}
			for _, want := range tc.mustKeep {
				if !strings.Contains(modelView, want) {
					t.Errorf("final compact response lost required diagnostic %q", want)
				}
			}
			if tc.centralDiagnostic != "" && strings.Contains(commandOutputCurrentView(tc.output), tc.centralDiagnostic) {
				t.Fatal("corpus error: the current head/tail baseline already contains the intended central failure")
			}
		})
	}
}

// Run with -v for a byte-count comparison over this fixed, equally weighted
// corpus. Timing is indicative only; BenchmarkCommandOutputCompact supplies
// standard Go benchmark timing and allocation measurements.
func TestCommandOutputCorpusMetrics(t *testing.T) {
	t.Logf("stdout sizes only, in bytes; proposed=current limit after compaction + %d-byte estimated recovery hint if savings reach 20%% and 256 B, otherwise current; no tokens/cost estimate", commandOutputRecoveryHintEstimateBytes)
	t.Log("filter timing excludes archival/hash/disk; estimated hint is not injected into the diagnostic-preservation check; corpus has no usage-frequency weighting")
	t.Log("case origin raw_B current_B proposed_B saved_vs_current_B saved_vs_current_pct filter_ns/op")
	var rawTotal, currentTotal, proposedTotal int
	const repeats = 100
	for _, tc := range commandOutputCorpus() {
		start := time.Now()
		var got string
		for i := 0; i < repeats; i++ {
			got, _ = compactCommandOutput(tc.command, tc.output)
		}
		nsPerOp := time.Since(start).Nanoseconds() / repeats
		raw := len(tc.output)
		current := len(commandOutputCurrentView(tc.output))
		proposed := commandOutputProposedViewBytes(tc.output, got)
		gain := current - proposed
		pct := float64(gain) * 100 / float64(current)
		t.Logf("%s %q %d %d %d %d %.2f %d", tc.name, tc.origin, raw, current, proposed, gain, pct, nsPerOp)
		rawTotal += raw
		currentTotal += current
		proposedTotal += proposed
	}
	gain := currentTotal - proposedTotal
	t.Logf("TOTAL equally-weighted-cases %d %d %d %d %.2f", rawTotal, currentTotal, proposedTotal, gain, float64(gain)*100/float64(currentTotal))
}

// Exercise the actual opt-in wrapper, including its real recovery notice,
// benefit gate, archive write, and final response. These are measured returned
// bytes rather than the 180-byte notice estimate used in the filter-only bench.
// nil execution errors keep the measurement's scope explicitly stdout only.
func TestCommandOutputCorpusRenderedResponse(t *testing.T) {
	t.Setenv("PRISM_COMPACT_COMMAND_OUTPUT", "1")
	t.Log("actual returned stdout bytes, including the real recovery hint; no tokens/cost estimate; each fixture counted once without usage-frequency weighting")
	t.Log("case raw_B current_B rendered_B hint_B saved_vs_current_B saved_vs_current_pct")
	var rawTotal, currentTotal, renderedTotal int
	for _, tc := range commandOutputCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			executor := ToolExecutor{workspaceDir: t.TempDir(), sessionID: "benchmark"}
			got := executor.finishCommandOutput(t.Context(), tc.command, tc.output, nil)
			baseline := commandOutputCurrentView(tc.output)
			if tc.unchanged && got != baseline {
				t.Fatal("actual wrapper changed the baseline response for unrecognized or structured output")
			}
			if tc.requireCompaction && len(got) >= len(baseline) {
				t.Errorf("actual wrapper did not reduce current response: current=%d rendered=%d", len(baseline), len(got))
			}
			for _, want := range tc.mustKeep {
				if !strings.Contains(got, want) {
					t.Errorf("actual rendered response lost required diagnostic %q", want)
				}
			}
			hintBytes := 0
			if got != baseline {
				compact, _ := compactCommandOutput(tc.command, tc.output)
				prefix := compactOutputPreview(compact)
				if !strings.HasPrefix(got, prefix) {
					t.Fatal("rendered compact response does not start with the filtered preview")
				}
				hintBytes = len(got) - len(prefix)
				archiveName := commandOutputName(executor.sessionID, tc.command, tc.output, "")
				if !strings.Contains(got, "/workspace/"+commandOutputDirectory+"/"+archiveName) {
					t.Error("actual compact response lost its recovery-file pointer")
				}
				archived, err := os.ReadFile(filepath.Join(executor.workspaceDir, commandOutputDirectory, archiveName))
				if err != nil {
					t.Fatalf("cannot read recovery archive: %v", err)
				}
				if string(archived) != tc.output {
					t.Error("recovery archive differs from full original stdout")
				}
			}
			raw, current, rendered := len(tc.output), len(baseline), len(got)
			gain := current - rendered
			t.Logf("%s %d %d %d %d %d %.2f", tc.name, raw, current, rendered, hintBytes, gain, float64(gain)*100/float64(current))
			rawTotal += raw
			currentTotal += current
			renderedTotal += rendered
		})
	}
	gain := currentTotal - renderedTotal
	t.Logf("TOTAL equally-weighted-cases %d %d %d %d %.2f", rawTotal, currentTotal, renderedTotal, gain, float64(gain)*100/float64(currentTotal))
}

var commandOutputBenchmarkSink string

func BenchmarkCommandOutputCompact(b *testing.B) {
	for _, tc := range commandOutputCorpus() {
		b.Run(tc.name, func(b *testing.B) {
			got, _ := compactCommandOutput(tc.command, tc.output)
			current := len(commandOutputCurrentView(tc.output))
			proposed := commandOutputProposedViewBytes(tc.output, got)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				commandOutputBenchmarkSink, _ = compactCommandOutput(tc.command, tc.output)
			}
			b.StopTimer()
			b.ReportMetric(float64(len(tc.output)), "raw_B/op")
			b.ReportMetric(float64(current), "current_B/op")
			b.ReportMetric(float64(proposed), "proposed_B/op")
			b.ReportMetric(float64(current-proposed), "saved_vs_current_B/op")
		})
	}
}
