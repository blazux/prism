package agent

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// commandCompactRunnerWords recognizes only wrappers that retain the runner's
// stdout: merging stderr, tee to ordinary literal file paths, and a final status
// echo. This is classification after execution, never a command rewrite. The
// footer and the original error stay in the output passed to the formatter.
func commandCompactRunnerWords(command string) ([]string, bool) {
	tokens, ok := commandCompactShellTokens(command)
	if !ok {
		return nil, false
	}
	if n := len(tokens); n >= 3 && (tokens[n-3] == ";" || tokens[n-3] == "&&") && tokens[n-2] == "echo" && commandCompactStatusEcho(tokens[n-1]) {
		tokens = tokens[:n-3]
	}
	pipe := -1
	for i, token := range tokens {
		if token == "|" {
			if pipe >= 0 {
				return nil, false
			}
			pipe = i
		}
	}
	if pipe >= 0 {
		if !commandCompactIdentityTee(tokens[pipe+1:]) {
			return nil, false
		}
		tokens = tokens[:pipe]
	}
	if n := len(tokens); n > 0 && tokens[n-1] == "2>&1" {
		tokens = tokens[:n-1]
	}
	// The original literal-word parser still rejects substitutions, redirects,
	// comments, unsupported syntax and ambiguous quoting in the runner itself.
	words, ok := commandCompactWords(strings.Join(tokens, " "))
	if !ok {
		return nil, false
	}
	runner := words
	if len(runner) >= 4 && runner[0] == "cd" && runner[1] != "" && !strings.HasPrefix(runner[1], "-") && runner[2] == "&&" {
		runner = runner[3:]
	}
	for _, word := range runner {
		if word == "&&" {
			return nil, false
		}
	}
	if !commandCompactGoCommand(runner) && !commandCompactNodeCommand(runner) {
		return nil, false
	}
	return words, true
}

// Token values retain their original quotes, so a quoted "|" or "2>&1" can
// never be confused with a shell operator. Only the few supported operators
// are recognized; this deliberately is not a general-purpose shell parser.
func commandCompactShellTokens(command string) ([]string, bool) {
	if !utf8.ValidString(command) || strings.ContainsAny(command, "\\`#") {
		return nil, false
	}
	for _, r := range command {
		if unicode.IsControl(r) && r != '\t' {
			return nil, false
		}
	}
	var tokens []string
	start := -1
	var quote byte
	flush := func(end int) {
		if start >= 0 {
			tokens = append(tokens, command[start:end])
			start = -1
		}
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			if start < 0 {
				start = i
			}
			quote = c
			continue
		}
		if c == ' ' || c == '\t' {
			flush(i)
			continue
		}
		if start < 0 && strings.HasPrefix(command[i:], "2>&1") {
			end := i + len("2>&1")
			if end < len(command) && !strings.ContainsRune(" \t;|&", rune(command[end])) {
				return nil, false
			}
			tokens = append(tokens, "2>&1")
			i = end - 1
			continue
		}
		switch c {
		case '&':
			if i+1 >= len(command) || command[i+1] != '&' {
				return nil, false
			}
			flush(i)
			tokens = append(tokens, "&&")
			i++
		case '|':
			if i+1 < len(command) && (command[i+1] == '|' || command[i+1] == '&') {
				return nil, false
			}
			flush(i)
			tokens = append(tokens, "|")
		case ';':
			flush(i)
			tokens = append(tokens, ";")
		case '<', '>':
			return nil, false
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush(len(command))
	return tokens, len(tokens) != 0
}

var commandCompactStatusWord = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*[:=]\$\?$`)

func commandCompactStatusEcho(word string) bool {
	_, ok := commandCompactStatusPrefix(word)
	return ok
}

func commandCompactStatusPrefix(word string) (string, bool) {
	if len(word) >= 2 && word[0] == '"' && word[len(word)-1] == '"' {
		word = word[1 : len(word)-1]
	} else if strings.ContainsAny(word, "\"'") {
		return "", false
	}
	if !commandCompactStatusWord.MatchString(word) {
		return "", false
	}
	return strings.TrimSuffix(word, "$?"), true
}

// commandCompactRunnerFooter separates a proven final shell status line from
// runner output before parsing. Its exact bytes are reattached after filtering;
// they are not a diagnostic belonging to the last completed Go/TAP test. This
// does not interpret the status as success/failure or alter the execution error.
func commandCompactRunnerFooter(command, output string) (body, footer string) {
	unchanged := func() (string, string) { return output, "" }
	if !strings.HasSuffix(output, "\n") {
		return unchanged()
	}
	tokens, ok := commandCompactShellTokens(command)
	if !ok || len(tokens) < 3 {
		return unchanged()
	}
	n := len(tokens)
	// With && the echo can be skipped after a failure, leaving a diagnostic
	// that happens to resemble a status line. Only an unconditional suffix is
	// separated. Conditional wrappers still use the conservative parser path.
	if tokens[n-3] != ";" || tokens[n-2] != "echo" {
		return unchanged()
	}
	prefix, ok := commandCompactStatusPrefix(tokens[n-1])
	if !ok {
		return unchanged()
	}
	if _, ok := commandCompactRunnerWords(command); !ok {
		return unchanged()
	}
	end := len(output) - 1
	if end > 0 && output[end-1] == '\r' {
		end--
	}
	start := strings.LastIndexByte(output[:end], '\n') + 1
	line := output[start:end]
	if !strings.HasPrefix(line, prefix) {
		return unchanged()
	}
	status := strings.TrimPrefix(line, prefix)
	// POSIX $? prints a canonical decimal status. Reject lookalikes such as
	// +0, 00, whitespace, a non-status suffix or a value outside 0..255.
	if len(status) == 0 || len(status) > 3 || (len(status) > 1 && status[0] == '0') {
		return unchanged()
	}
	for _, c := range status {
		if c < '0' || c > '9' {
			return unchanged()
		}
	}
	code, err := strconv.Atoi(status)
	if err != nil || code > 255 {
		return unchanged()
	}
	return output[:start], output[start:]
}

func commandCompactIdentityTee(tokens []string) bool {
	words, ok := commandCompactWords(strings.Join(tokens, " "))
	if !ok || len(words) < 2 || words[0] != "tee" {
		return false
	}
	operands := words[1:]
	if operands[0] == "-a" || operands[0] == "--append" {
		operands = operands[1:]
	}
	if len(operands) > 0 && operands[0] == "--" {
		operands = operands[1:]
	}
	if len(operands) == 0 {
		return false
	}
	for _, file := range operands {
		if file == "" || strings.HasPrefix(file, "-") || file == "&&" {
			return false
		}
		// Standard-output aliases can duplicate the stream. Device/procfs
		// operands are outside the ordinary-file tee case supported here.
		clean := "/" + strings.TrimPrefix(path.Clean(file), "/")
		for _, special := range []string{"/dev/", "/proc/", "/sys/"} {
			if strings.Contains(clean+"/", special) {
				return false
			}
		}
	}
	return true
}
