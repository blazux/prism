package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	commandOutputDirectory = ".prism-command-output"
	commandOutputMaxBytes  = 2 * 1024 * 1024
	commandOutputPerScope  = 32
	commandOutputTotal     = 128
)

var commandOutputFilesMu sync.Mutex
var commandOutputFilename = regexp.MustCompile(`^[0-9a-f]{16}-[0-9a-f]{64}\.log$`)

// Keep the existing baseline, including its byte budget, when the experiment
// is disabled, the format is unknown or safe recovery cannot be provided.
func commandOutputPreview(output string) string {
	if len(output) > 8000 {
		return output[:4000] + "\n...[truncated]...\n" + output[len(output)-4000:]
	}
	return output
}

func compactOutputPreview(output string) string {
	if len(output) <= 8000 {
		return output
	}
	head, tail := 4000, len(output)-4000
	for head > 0 && !utf8.RuneStart(output[head]) {
		head--
	}
	for tail < len(output) && !utf8.RuneStart(output[tail]) {
		tail++
	}
	return output[:head] + "\n...[truncated]...\n" + output[tail:]
}

type commandOutputStats struct {
	mode, filter                                  string
	rawBytes, errorBytes, beforeBytes, afterBytes int
}

// finishCommandOutput runs only after the original command has executed. It
// does not rewrite shell commands, retry them, or change their error identity.
func (e *ToolExecutor) finishCommandOutput(ctx context.Context, command, output string, execErr error) string {
	if e.rawResults {
		return output
	}
	baseline := commandOutputPreview(output)
	if os.Getenv("PRISM_COMPACT_COMMAND_OUTPUT") != "1" {
		return baseline
	}
	errorText := ""
	if execErr != nil {
		errorText = execErr.Error()
	}
	messageBytes := func(text string) int {
		if execErr == nil {
			return len(text)
		}
		n := len("Error: ") + len(errorText)
		if text != "" {
			n += len("\nOutput: ") + len(text)
		}
		return n
	}
	stats := commandOutputStats{mode: "unchanged", rawBytes: len(output), errorBytes: len(errorText), beforeBytes: messageBytes(baseline)}
	stats.afterBytes = stats.beforeBytes
	defer func() { e.recordCommandOutput(ctx, stats) }()
	if len(output)+len(errorText)+64 > commandOutputMaxBytes || !utf8.ValidString(output) || !utf8.ValidString(errorText) || !utf8.ValidString(command) || strings.ContainsRune(output, 0) {
		stats.mode = "unsupported"
		return baseline
	}
	compact, filter := compactCommandOutput(command, output)
	if filter == "" || compact == output {
		return baseline
	}
	stats.filter = filter
	name := commandOutputName(e.sessionID, command, output, errorText)
	hint := fmt.Sprintf("\n[Prism: %s output compacted; full output: /workspace/%s/%s]\n", filter, commandOutputDirectory, name)
	result := compactOutputPreview(compact) + hint
	// Compare against what Prism already sent, not against an uncapped terminal
	// log. Include the unchanged error and recovery pointer in the comparison.
	after := messageBytes(result)
	saved := stats.beforeBytes - after
	if saved < 256 || saved*5 < stats.beforeBytes {
		stats.mode = "no_gain"
		return baseline
	}
	archive := output
	if errorText != "" {
		archive += "\n\n[Prism execution error]\n" + errorText
	}
	if err := e.storeCommandOutput(name, []byte(archive)); err != nil {
		stats.mode = "recovery_unavailable"
		return baseline
	}
	stats.mode, stats.afterBytes = "compact", after
	return result
}

func commandOutputName(session, command, output, errorText string) string {
	scope := sha256.Sum256([]byte(session))
	// A structured tuple avoids ambiguous concatenations. The original command
	// is hashed, never written to the recovery file or the usage event.
	identity, _ := json.Marshal([3]string{command, output, errorText})
	content := sha256.Sum256(identity)
	return hex.EncodeToString(scope[:8]) + "-" + hex.EncodeToString(content[:]) + ".log"
}

func (e *ToolExecutor) recordCommandOutput(ctx context.Context, stats commandOutputStats) {
	log.Printf("[command-output] session=%s mode=%s filter=%s stdout_bytes=%d error_bytes=%d baseline_bytes=%d returned_bytes=%d",
		e.sessionID, stats.mode, stats.filter, stats.rawBytes, stats.errorBytes, stats.beforeBytes, stats.afterBytes)
	if e.memStore != nil {
		e.memStore.AddUsage(ctx, 0, e.sessionID, "command_output", "exec_command", 1, map[string]interface{}{
			"mode": stats.mode, "filter": stats.filter,
			"stdout_bytes": stats.rawBytes, "error_bytes": stats.errorBytes,
			"baseline_bytes": stats.beforeBytes, "returned_bytes": stats.afterBytes,
			"saved_bytes": stats.beforeBytes - stats.afterBytes,
		})
	}
}

// Recovery uses the execution volume's existing file access contract. A hashed
// scope organizes files; it is not a private-files boundary between boards.
func (e *ToolExecutor) storeCommandOutput(name string, content []byte) error {
	if e.workspaceDir == "" || len(content) > commandOutputMaxBytes || !commandOutputFilename.MatchString(name) {
		return fmt.Errorf("command output cannot be archived")
	}
	commandOutputFilesMu.Lock()
	defer commandOutputFilesMu.Unlock()
	root, err := os.OpenRoot(e.workspaceDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(commandOutputDirectory, 0755); err != nil {
		return err
	}
	info, err := root.Lstat(commandOutputDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("command output cache is not a directory")
	}
	directory, err := root.OpenRoot(commandOutputDirectory)
	if err != nil {
		return err
	}
	defer directory.Close()
	f, err := directory.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		// The agent can edit workspace files. Never claim that a reused path is a
		// recoverable copy without checking the bytes currently stored there.
		info, err := directory.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("command output cache entry is not a regular file")
		}
		existing, err := directory.Open(name)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(existing, commandOutputMaxBytes+1))
		closeErr := existing.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(data, content) {
			return fmt.Errorf("command output cache entry differs")
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = directory.Remove(name)
		return fmt.Errorf("command output cache write failed")
	}
	if err := pruneCommandOutput(directory, name); err != nil {
		_ = directory.Remove(name)
		return err
	}
	return nil
}

func pruneCommandOutput(root *os.Root, current string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return err
	}
	type archive struct {
		name string
		info os.FileInfo
	}
	var files []archive
	scope := current[:16]
	scopeCount := 0
	for _, entry := range entries {
		if !commandOutputFilename.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, archive{entry.Name(), info})
		if strings.HasPrefix(entry.Name(), scope+"-") {
			scopeCount++
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].info.ModTime().Equal(files[j].info.ModTime()) {
			return files[i].name < files[j].name
		}
		return files[i].info.ModTime().Before(files[j].info.ModTime())
	})
	total := len(files)
	removed := make(map[string]bool)
	remove := func(file archive) error {
		if err := root.Remove(file.name); err != nil && !os.IsNotExist(err) {
			return err
		}
		removed[file.name] = true
		total--
		if strings.HasPrefix(file.name, scope+"-") {
			scopeCount--
		}
		return nil
	}
	// Satisfy the session bound first: that eviction may also satisfy the
	// installation-wide bound, without discarding another session's file.
	for _, file := range files {
		if scopeCount <= commandOutputPerScope {
			break
		}
		if file.name != current && strings.HasPrefix(file.name, scope+"-") {
			if err := remove(file); err != nil {
				return err
			}
		}
	}
	for _, file := range files {
		if total <= commandOutputTotal {
			break
		}
		if file.name != current && !removed[file.name] {
			if err := remove(file); err != nil {
				return err
			}
		}
	}
	return nil
}
