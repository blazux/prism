package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// compactCommandOutput recognizes a small set of test runner transcripts. It
// never runs a command, and an empty filter means output is returned byte for
// byte. Unknown lines are retained together with any test contexts they could
// belong to; only complete, quiet successes are reduced to counters.
func compactCommandOutput(command, output string) (text string, filter string) {
	if !commandCompactText(output) {
		return output, ""
	}
	words, ok := commandCompactRunnerWords(command)
	if !ok {
		return output, ""
	}
	if len(words) >= 4 && words[0] == "cd" && words[1] != "" && !strings.HasPrefix(words[1], "-") && words[2] == "&&" {
		words = words[3:]
	}
	for _, word := range words {
		if word == "&&" {
			return output, ""
		}
	}
	// A terminal status echo comes from the admitted shell wrapper, rather
	// than an unattributed runner diagnostic. Parse the runner independently
	// and retain the exact footer in the model-visible result and raw archive.
	body, footer := commandCompactRunnerFooter(command, output)
	if commandCompactGoCommand(words) {
		if compact := commandCompactGo(body); len(compact) < len(body) {
			return compact + footer, "go-test"
		}
	}
	if commandCompactNodeCommand(words) {
		if compact := commandCompactTAP(body); len(compact) < len(body) {
			return compact + footer, "tap"
		}
	}
	return output, ""
}

func commandCompactText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return false
	}
	for i, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
		if r == '\r' && (i+1 == len(s) || s[i+1] != '\n') {
			return false
		}
	}
	return true
}

// This is deliberately not a shell lexer. Literal quoted words and one cd &&
// prefix are sufficient; expansions and all other shell syntax are rejected.
func commandCompactWords(command string) ([]string, bool) {
	if !utf8.ValidString(command) || strings.ContainsAny(command, "\n\r;|<>`$(){}\\#") {
		return nil, false
	}
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if unicode.IsControl(r) && r != '\t' {
			return nil, false
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else if r == '&' {
				return nil, false
			} else {
				word.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t':
			flush()
		case r == '&':
			if i+1 >= len(runes) || runes[i+1] != '&' {
				return nil, false
			}
			flush()
			words = append(words, "&&")
			i++
		default:
			// Globs and tilde expansion make a word depend on the shell.
			if strings.ContainsRune("*?[]~", r) {
				return nil, false
			}
			word.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return words, len(words) != 0
}

func commandCompactGoCommand(words []string) bool {
	if len(words) < 2 || words[0] != "go" || words[1] != "test" {
		return false
	}
	boolFlags := map[string]bool{"-v": true, "-race": true, "-short": true, "-failfast": true, "-cover": true, "-trimpath": true}
	valueFlags := map[string]bool{"-run": true, "-count": true, "-timeout": true, "-parallel": true, "-p": true, "-shuffle": true, "-cpu": true, "-vet": true, "-tags": true, "-covermode": true, "-coverpkg": true}
	for i := 2; i < len(words); i++ {
		word := words[i]
		if word == "" {
			return false
		}
		if !strings.HasPrefix(word, "-") {
			continue
		}
		name, value, hasValue := strings.Cut(word, "=")
		if boolFlags[name] {
			if hasValue && value != "true" && value != "false" {
				return false
			}
			continue
		}
		if !valueFlags[name] {
			return false
		}
		if !hasValue {
			i++
			if i == len(words) || words[i] == "" || strings.HasPrefix(words[i], "-") {
				return false
			}
		} else if value == "" {
			return false
		}
	}
	return true
}

func commandCompactNodeCommand(words []string) bool {
	if len(words) < 2 || words[0] != "node" {
		return false
	}
	seenTest, seenFile := false, false
	for i := 1; i < len(words); i++ {
		word := words[i]
		if word == "" {
			return false
		}
		if !strings.HasPrefix(word, "-") {
			if !seenTest {
				return false
			}
			seenFile = true
			continue
		}
		if seenFile {
			return false
		}
		name, value, hasValue := strings.Cut(word, "=")
		if name == "--test" || name == "--test-only" {
			if hasValue {
				return false
			}
			seenTest = seenTest || name == "--test"
			continue
		}
		switch name {
		case "--test-reporter", "--test-name-pattern", "--test-skip-pattern", "--test-concurrency", "--test-timeout", "--test-shard":
			if !hasValue {
				i++
				if i == len(words) || words[i] == "" || strings.HasPrefix(words[i], "-") {
					return false
				}
				value = words[i]
			}
			if value == "" || (name == "--test-reporter" && value != "tap") {
				return false
			}
		default:
			return false
		}
	}
	return seenTest
}

type commandCompactLine struct{ raw, text string }

func commandCompactLines(output string) []commandCompactLine {
	parts := strings.SplitAfter(output, "\n")
	lines := make([]commandCompactLine, 0, len(parts))
	for _, raw := range parts {
		if raw != "" {
			lines = append(lines, commandCompactLine{raw, strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")})
		}
	}
	return lines
}

func commandCompactJoin(lines []commandCompactLine, removed []bool, summary string) string {
	var b strings.Builder
	b.WriteString(summary)
	b.WriteByte('\n')
	for i, line := range lines {
		if !removed[i] {
			b.WriteString(line.raw)
		}
	}
	return b.String()
}

var (
	commandCompactGoHeader  = regexp.MustCompile(`^=== (?:RUN   |PAUSE |CONT  |NAME  )([^[:space:]]+)$`)
	commandCompactGoResult  = regexp.MustCompile(`^ *--- (PASS|FAIL|SKIP): ([^[:space:]]+) \([0-9]+(?:\.[0-9]+)?s\)$`)
	commandCompactGoLog     = regexp.MustCompile(`^ +[^\r\n]+\.go:[0-9]+: ?`)
	commandCompactGoPackage = regexp.MustCompile(`^(?:ok[ \t]+[^[:space:]]+[ \t]+(?:[0-9]+(?:\.[0-9]+)?s|\(cached\))(?:[ \t]+coverage: .*)?|FAIL[ \t]+[^[:space:]]+[ \t]+(?:[0-9]+(?:\.[0-9]+)?s|\[build failed\]|\[setup failed\])|\?[ \t]+[^[:space:]]+[ \t]+\[no test files\])$`)
)

type commandCompactGoCase struct {
	name            string
	headers, passes []int
	keep            bool
	parent          *commandCompactGoCase
}

func commandCompactGo(output string) string {
	lines := commandCompactLines(output)
	for _, line := range lines {
		if strings.TrimSpace(line.text) == "" {
			continue
		}
		if !strings.HasPrefix(line.text, "=== RUN   ") && !commandCompactGoPackage.MatchString(line.text) {
			// TestMain stdout before any runner scope cannot be attributed.
			return output
		}
		break
	}
	// Go can buffer child result lines until their parent finishes. Those
	// children are awaiting a result, but are no longer the current test: a
	// later sibling's t.Log must not keep every previous quiet sibling.
	var cases []*commandCompactGoCase
	lookup := make(map[string]*commandCompactGoCase)
	pending := make(map[string]*commandCompactGoCase)
	var current, lastResult *commandCompactGoCase
	pendingUnknown, parallel, logContinuation := false, false, false
	packageSeen := false
	observedPass, observedFail, observedSkip, topLevel, subtests := 0, 0, 0, 0, 0
	keepContext := func(c *commandCompactGoCase) {
		for ; c != nil; c = c.parent {
			c.keep = true
		}
	}
	for i, line := range lines {
		if m := commandCompactGoHeader.FindStringSubmatch(line.text); m != nil {
			c := lookup[m[1]]
			if strings.HasPrefix(line.text, "=== RUN   ") {
				if pending[m[1]] != nil {
					// Overlapping packages or an unrecognized runner sequence.
					return output
				}
				if c != nil {
					// A new -count iteration starts a distinct generation. Do not
					// link its logs or parents to the previous iteration's names.
					if strings.Contains(m[1], "/") || len(pending) != 0 {
						return output
					}
					lookup = make(map[string]*commandCompactGoCase)
				}
				c = &commandCompactGoCase{name: m[1], keep: pendingUnknown}
				for name := c.name; strings.Contains(name, "/"); {
					name = name[:strings.LastIndexByte(name, '/')]
					if parent := pending[name]; parent != nil {
						c.parent = parent
						break
					}
				}
				if strings.Contains(c.name, "/") && c.parent == nil {
					return output // a nested scope without its runner parent
				}
				cases = append(cases, c)
				lookup[c.name], pending[c.name] = c, c
				pendingUnknown = false
			}
			if c == nil {
				return output // CONT/NAME/PAUSE without a known RUN
			}
			if strings.HasPrefix(line.text, "=== PAUSE ") || strings.HasPrefix(line.text, "=== CONT  ") {
				if pending[c.name] == nil {
					return output
				}
				parallel = true
			}
			c.headers = append(c.headers, i)
			current, logContinuation = c, false
			continue
		}
		if m := commandCompactGoResult.FindStringSubmatch(line.text); m != nil {
			if strings.Contains(m[2], "/") {
				subtests++
			} else {
				topLevel++
			}
			switch m[1] {
			case "PASS":
				observedPass++
			case "FAIL":
				observedFail++
			case "SKIP":
				observedSkip++
			}
			if c := pending[m[2]]; c != nil {
				if m[1] == "PASS" {
					c.passes = append(c.passes, i)
				} else {
					keepContext(c)
				}
				delete(pending, m[2])
				lastResult = c
				current, logContinuation = nil, false
				continue
			}
		}
		if commandCompactGoPackage.MatchString(line.text) {
			packageSeen = true
			for _, c := range pending {
				keepContext(c)
			}
			// Names can repeat between packages without sharing a context.
			lookup = make(map[string]*commandCompactGoCase)
			pending = make(map[string]*commandCompactGoCase)
			current, parallel, logContinuation = nil, false, false
			continue
		}
		if line.text == "PASS" && i+1 < len(lines) && strings.HasPrefix(lines[i+1].text, "ok") && commandCompactGoPackage.MatchString(lines[i+1].text) {
			continue
		}
		if line.text == "FAIL" && len(pending) == 0 {
			continue
		}
		if strings.TrimSpace(line.text) != "" {
			attributed := commandCompactGoLog.MatchString(line.text) || (logContinuation && strings.HasPrefix(line.text, "        "))
			if !parallel && current != nil && attributed {
				keepContext(current)
				logContinuation = true
			} else {
				// Raw stdout, panic output, parallel logs and unknown placement
				// can have another source. Retain every possible pending scope.
				for _, c := range pending {
					keepContext(c)
				}
				keepContext(current)
				keepContext(lastResult)
				pendingUnknown = true
				logContinuation = false
			}
		}
	}
	if !packageSeen {
		return output
	}
	for _, c := range pending {
		keepContext(c)
	}
	for _, c := range cases {
		if c.keep || len(c.passes) == 0 {
			keepContext(c)
		}
	}
	removed := make([]bool, len(lines))
	passes, headers, markers := 0, 0, 0
	for _, c := range cases {
		if c.keep || len(c.passes) == 0 {
			continue
		}
		for _, i := range c.headers {
			removed[i] = true
			headers++
		}
		for _, i := range c.passes {
			removed[i] = true
			passes++
		}
	}
	if passes == 0 {
		return output
	}
	for i, line := range lines {
		if line.text == "PASS" && i+1 < len(lines) && strings.HasPrefix(lines[i+1].text, "ok") && commandCompactGoPackage.MatchString(lines[i+1].text) {
			removed[i] = true
			markers++
		}
	}
	return commandCompactJoin(lines, removed, fmt.Sprintf("[go test: omitted %d passing result lines, %d progress lines, %d PASS markers; observed results: pass=%d fail=%d skip=%d top-level=%d subtests=%d]", passes, headers, markers, observedPass, observedFail, observedSkip, topLevel, subtests))
}

var (
	commandCompactTAPHeader        = regexp.MustCompile(`^( *)# Subtest: (.*)$`)
	commandCompactTAPResult        = regexp.MustCompile(`^( *)(not ok|ok) [1-9][0-9]*(?: - (.*))?$`)
	commandCompactTAPPlan          = regexp.MustCompile(`^( *)1\.\.([0-9]+)$`)
	commandCompactTAPCounter       = regexp.MustCompile(`^# (tests|suites|pass|fail|cancelled|skipped|todo) ([0-9]+)$`)
	commandCompactTAPTotalDuration = regexp.MustCompile(`^# duration_ms [0-9]+(?:\.[0-9]+)?$`)
	commandCompactTAPDirective     = regexp.MustCompile(`(?i)^(.*?)[ \t]+#[ \t]+(?:SKIP|TODO)(?:[ \t].*)?$`)
	commandCompactTAPDuration      = regexp.MustCompile(`^duration_ms: [0-9]+(?:\.[0-9]+)?$`)
	commandCompactTAPType          = regexp.MustCompile(`^type: (?:'test'|'suite'|"test"|"suite")$`)
)

type commandCompactTAPCase struct {
	name                             string
	indent, header, result, children int
	plan, planCount                  int
	metadata                         []int
	keep                             bool
	firstIndex                       int
	ambiguous                        bool
	lastChild                        *commandCompactTAPCase
	pendingUnknown                   bool
}

func commandCompactTAP(output string) string {
	lines := commandCompactLines(output)
	for _, line := range lines {
		if strings.TrimSpace(line.text) == "" {
			continue
		}
		if line.text != "TAP version 13" {
			return output
		}
		break
	}
	var stack, cases []*commandCompactTAPCase
	var lastTop *commandCompactTAPCase
	pendingUnknown := false
	rootPlan, rootResults, version := -1, 0, false
	counters := make(map[string]bool)
	for i := 0; i < len(lines); i++ {
		line := lines[i].text
		if line == "TAP version 13" {
			if version || len(stack) != 0 || len(cases) != 0 {
				return output
			}
			version = true
			continue
		}
		if m := commandCompactTAPHeader.FindStringSubmatch(line); m != nil {
			if !version || (len(stack) == 0 && len(m[1]) != 0) || (len(stack) != 0 && len(m[1]) <= stack[len(stack)-1].indent) {
				return output
			}
			c := &commandCompactTAPCase{name: m[2], indent: len(m[1]), header: i, result: -1, plan: -1, firstIndex: len(cases)}
			if len(stack) == 0 {
				c.ambiguous = pendingUnknown
				pendingUnknown = false
			} else {
				parent := stack[len(stack)-1]
				c.ambiguous = parent.ambiguous || parent.pendingUnknown
				parent.pendingUnknown = false
			}
			// File-level test names can be the only occurrence of their path.
			c.keep = c.ambiguous || commandCompactPathName(c.name)
			stack = append(stack, c)
			cases = append(cases, c)
			continue
		}
		if m := commandCompactTAPResult.FindStringSubmatch(line); m != nil {
			if len(stack) == 0 {
				return output
			}
			c := stack[len(stack)-1]
			name, directive := m[3], false
			if d := commandCompactTAPDirective.FindStringSubmatch(name); d != nil {
				name, directive = d[1], true
			}
			if len(m[1]) != c.indent || name != c.name || (c.plan >= 0 && c.planCount != c.children) || (c.children != 0 && c.plan < 0) {
				return output
			}
			c.result = i
			c.keep = c.keep || m[2] != "ok" || directive
			if i+1 < len(lines) && lines[i+1].text == strings.Repeat(" ", c.indent+2)+"---" {
				end, known := commandCompactTAPMetadata(lines, i+1, c.indent)
				if end < 0 {
					return output
				}
				c.keep = c.keep || !known
				for j := i + 1; j <= end; j++ {
					c.metadata = append(c.metadata, j)
				}
				i = end
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				rootResults++
				lastTop = c
			} else {
				parent := stack[len(stack)-1]
				parent.children++
				parent.keep = parent.keep || c.keep
				parent.lastChild = c
			}
			continue
		}
		if m := commandCompactTAPPlan.FindStringSubmatch(line); m != nil {
			count, err := strconv.Atoi(m[2])
			if err != nil || !version {
				return output
			}
			if len(stack) == 0 {
				if len(m[1]) != 0 || rootPlan >= 0 {
					return output
				}
				rootPlan = count
			} else {
				c := stack[len(stack)-1]
				if len(m[1]) <= c.indent || c.plan >= 0 {
					return output
				}
				c.plan, c.planCount = i, count
			}
			continue
		}
		if m := commandCompactTAPCounter.FindStringSubmatch(line); m != nil && len(stack) == 0 {
			if counters[m[1]] {
				return output
			}
			counters[m[1]] = true
			continue
		}
		if len(stack) == 0 && commandCompactTAPTotalDuration.MatchString(line) {
			continue
		}
		if strings.TrimSpace(line) != "" {
			// Node may buffer stdout from any test before its first Subtest.
			// There is no reliable neighboring context for this preamble.
			if len(cases) == 0 {
				return output
			}
			for _, c := range stack {
				c.keep = true
			}
			if len(stack) == 0 {
				// File stdout can precede its Subtest header. Without an active
				// scope, retain both adjacent test scopes instead of assigning it.
				if lastTop != nil {
					for _, c := range cases[lastTop.firstIndex:] {
						c.keep = true
					}
				}
				pendingUnknown = true
			} else {
				// Node emits t.diagnostic() after the child's result and YAML,
				// while only its parent is open. Preserve sibling contexts at
				// this depth just as for unattributed file stdout at the root.
				parent := stack[len(stack)-1]
				if parent.lastChild != nil {
					for _, c := range cases[parent.lastChild.firstIndex:] {
						c.keep = true
					}
				}
				parent.pendingUnknown = true
			}
		}
	}
	if !version || len(stack) != 0 || rootPlan != rootResults || !counters["tests"] || !counters["pass"] || !counters["fail"] {
		return output
	}
	removed := make([]bool, len(lines))
	passes, metadata, plans := 0, 0, 0
	for _, c := range cases {
		if c.keep || c.result < 0 {
			continue
		}
		removed[c.header], removed[c.result] = true, true
		passes++
		for _, i := range c.metadata {
			removed[i] = true
			metadata++
		}
		if c.plan >= 0 {
			removed[c.plan] = true
			plans++
		}
	}
	if passes == 0 {
		return output
	}
	return commandCompactJoin(lines, removed, fmt.Sprintf("[TAP: omitted %d passing result lines, %d subtest headers, %d timing/type metadata lines, %d nested plans]", passes, passes, metadata, plans))
}

func commandCompactTAPMetadata(lines []commandCompactLine, start, indent int) (end int, known bool) {
	closing, prefix := strings.Repeat(" ", indent+2)+"...", strings.Repeat(" ", indent+2)
	duration, testType, known := false, false, true
	for i := start + 1; i < len(lines); i++ {
		line := lines[i].text
		if line == closing {
			return i, known && duration
		}
		if !strings.HasPrefix(line, prefix) {
			known = false
			continue
		}
		value := strings.TrimPrefix(line, prefix)
		switch {
		case commandCompactTAPDuration.MatchString(value) && !duration:
			duration = true
		case commandCompactTAPType.MatchString(value) && !testType:
			testType = true
		default:
			known = false
		}
	}
	return -1, false
}

func commandCompactPathName(name string) bool {
	if strings.ContainsAny(name, `/\`) {
		return true
	}
	name = strings.ToLower(name)
	for _, ext := range []string{".js", ".mjs", ".cjs", ".ts", ".mts", ".cts"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}
