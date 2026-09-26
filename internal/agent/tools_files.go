package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"prism/internal/workspace"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func (e *ToolExecutor) downloadFile(ctx context.Context, rawURL, path string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid URL: must be http or https")
	}
	fullPath := filepath.Join(e.workspaceDir, filepath.Clean("/"+NormalizeWorkspacePath(path)))
	rel, err := filepath.Rel(e.workspaceDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes workspace")
	}
	if e.isProtectedToolPath(fullPath) {
		return "", fmt.Errorf("%q is a tool shipped with Prism and cannot be overwritten", filepath.Base(fullPath))
	}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		// Same transport-error rewrite as http_request: a guessed non-existent
		// host must read as NXDOMAIN, not as a flaky Docker DNS resolver.
		return "", httpRequestError(u.Hostname(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}
	written, err := workspace.Write(e.workspaceDir, NormalizeWorkspacePath(path), resp.Body)
	if err != nil {
		return "", fmt.Errorf("write failed: %w", err)
	}
	if e.onFileChange != nil {
		e.onFileChange()
	}
	return fmt.Sprintf("Downloaded %d bytes → %s", written, path), nil
}

func (e *ToolExecutor) execCommand(ctx context.Context, command string) (string, error) {
	session := e.sessionID
	if session == "" {
		session = "default"
	}
	env := map[string]string{
		"PRISM_SESSION": session,
		"PRISM_URL":     "http://prism-server:8080",
		"PRISM_TOKEN":   e.prismToken,
	}
	// Personal secrets plus the group's shared tier (personal wins on a name
	// collision); reserved integration credentials never reach the env.
	secretEnv, err := e.secretsEnv(ctx)
	if err != nil {
		return "", err
	}
	for name, value := range secretEnv {
		env[name] = value
	}
	out, err := e.docker.ExecWithEnv(ctx, "cd /workspace && "+command, 2*time.Minute, env)
	if len(out) > 8000 {
		out = out[:4000] + "\n...[truncated]...\n" + out[len(out)-4000:]
	}
	if err != nil {
		return fmt.Sprintf("ERROR: %v\nOutput: %s", err, out), nil
	}
	return out, nil
}

// NormalizeWorkspacePath strips the /workspace prefix that models often include
// when they should be passing a path relative to the workspace root.
func NormalizeWorkspacePath(path string) string {
	path = strings.TrimPrefix(path, "/workspace/")
	path = strings.TrimPrefix(path, "/workspace")
	if path == "" {
		path = "."
	}
	return path
}

func (e *ToolExecutor) writeFile(path, content string) (string, error) {
	path = filepath.Clean(NormalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}

	fullPath := filepath.Join(e.workspaceDir, path)
	if e.isProtectedToolPath(fullPath) {
		return "", fmt.Errorf("%q is a tool shipped with Prism and cannot be overwritten", filepath.Base(fullPath))
	}
	if err := workspace.WriteFile(e.workspaceDir, path, []byte(content)); err != nil {
		return "", err
	}

	if e.onFileChange != nil {
		e.onFileChange()
	}
	return fmt.Sprintf("Written %d bytes to %s", len(content), path), nil
}

// editFile replaces an exact text fragment in a workspace file. By default a
// replacement must be unambiguous: refusing to guess is safer than changing
// several similar lines. Callers can opt into replacing every occurrence with
// replaceAll=true.
func (e *ToolExecutor) editFile(path, oldText, newText string, replaceAll bool) (string, error) {
	path = filepath.Clean(NormalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}
	if oldText == "" {
		return "", fmt.Errorf("old_text is required; read the file and provide the exact text to replace")
	}

	fullPath := filepath.Join(e.workspaceDir, path)
	if e.isProtectedToolPath(fullPath) {
		return "", fmt.Errorf("%q is a tool shipped with Prism and cannot be edited", filepath.Base(fullPath))
	}
	data, err := workspace.ReadFile(e.workspaceDir, path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", e.notFoundHint(path, fullPath)
		}
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not a UTF-8 text file; use exec_command for binary files", path)
	}
	content := string(data)
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", fmt.Errorf("old_text was not found in %s; read the current file and retry with an exact match", path)
	}
	if count > 1 && !replaceAll {
		return "", fmt.Errorf("old_text occurs %d times in %s; provide a larger unique fragment or set replace_all=true", count, path)
	}
	updated := strings.Replace(content, oldText, newText, 1)
	if replaceAll {
		updated = strings.ReplaceAll(content, oldText, newText)
	}
	if err := workspace.WriteFile(e.workspaceDir, path, []byte(updated)); err != nil {
		return "", err
	}
	if e.onFileChange != nil {
		e.onFileChange()
	}
	suffix := ""
	if count != 1 {
		suffix = "s"
	}
	return fmt.Sprintf("Edited %s (%d replacement%s)", path, count, suffix), nil
}

func (e *ToolExecutor) readFile(path string) (string, error) {
	path = filepath.Clean(NormalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}

	fullPath := filepath.Join(e.workspaceDir, path)
	data, err := workspace.ReadFile(e.workspaceDir, path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", e.notFoundHint(path, fullPath)
		}
		return "", err
	}
	content := string(data)
	if len(content) > 10000 {
		content = content[:10000] + fmt.Sprintf("\n...[truncated at 10000 chars — read the rest with exec_command, e.g. `sed -n '200,400p' %s`]", path)
	}
	return content, nil
}

// notFoundHint turns a bare "no such file" into an error that teaches: it lists
// the sibling files in the target directory and suggests the closest name, so a
// typo or a wrong-directory guess costs the agent one message instead of several
// probing round-trips (the "errors that teach" pattern). path is the
// workspace-relative path the agent passed; fullPath is the resolved host path.
func (e *ToolExecutor) notFoundHint(path, fullPath string) error {
	relDir := filepath.Dir(path)
	entries, derr := workspace.ReadDir(e.workspaceDir, relDir)
	if derr != nil {
		return fmt.Errorf("file not found: %s — its directory %q does not exist either; check the path", path, relDir)
	}
	names := make([]string, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			names = append(names, ent.Name()+"/")
		} else {
			names = append(names, ent.Name())
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("file not found: %s — directory %q is empty", path, relDir)
	}
	sort.Strings(names)

	msg := "file not found: " + path
	if best := closestName(filepath.Base(path), names); best != "" {
		msg += fmt.Sprintf(" — did you mean %s?", filepath.Join(relDir, best))
	}
	const maxList = 40
	extra := 0
	if len(names) > maxList {
		extra = len(names) - maxList
		names = names[:maxList]
	}
	msg += fmt.Sprintf("\nFiles in %q: %s", relDir, strings.Join(names, ", "))
	if extra > 0 {
		msg += fmt.Sprintf(" (+%d more)", extra)
	}
	return errors.New(msg)
}

// closestName returns the directory entry closest to target by edit distance,
// but only when it's a plausible typo (distance within 3 and half the name
// length) — otherwise "" so we never suggest an unrelated file.
func closestName(target string, names []string) string {
	target = strings.ToLower(target)
	best, bestDist := "", 1<<30
	for _, n := range names {
		if d := levenshtein(strings.ToLower(strings.TrimSuffix(n, "/")), target); d < bestDist {
			best, bestDist = strings.TrimSuffix(n, "/"), d
		}
	}
	limit := len(target) / 2
	if limit > 3 {
		limit = 3
	}
	if best == "" || bestDist > limit {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = minInt(prev[j]+1, minInt(cur[j-1]+1, prev[j-1]+cost))
		}
		prev = cur
	}
	return prev[len(b)]
}

func minInt(a, b int) int {
	if b < a {
		return b
	}
	return a
}

// isProtectedToolPath reports whether fullPath points directly at a custom
// tool file marked "protected" in its own "# TOOL: {...}" header (e.g. the
// embedded pcap reader) — one shared check for every path by which a file
// under agent_tools/ could otherwise be destroyed: delete_file, write_file's
// overwrite, and wget/download_file's overwrite. exec_command's shell is
// deliberately NOT gated here — sandboxing a generic shell against one
// directory is fragile (trivially bypassed via mv/python os.remove/etc.) and
// against this deployment's trusted-shell model; this closes the structured,
// path-based tools instead. Agent-created (non-protected) tools are
// unaffected by any of these checks.
func (e *ToolExecutor) isProtectedToolPath(fullPath string) bool {
	return e.customMgr != nil && filepath.Dir(fullPath) == e.customMgr.Dir() && e.customMgr.IsProtectedFilename(filepath.Base(fullPath))
}

func (e *ToolExecutor) deleteFile(path string) (string, error) {
	path = filepath.Clean(NormalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}
	fullPath := filepath.Join(e.workspaceDir, path)
	if e.isProtectedToolPath(fullPath) {
		return "", fmt.Errorf("%q is a tool shipped with Prism and cannot be deleted", filepath.Base(fullPath))
	}
	if err := workspace.Remove(e.workspaceDir, path); err != nil {
		return "", err
	}
	if e.onFileChange != nil {
		e.onFileChange()
	}
	return fmt.Sprintf("Deleted %s", path), nil
}

func (e *ToolExecutor) listFiles(path string) (string, error) {
	path = filepath.Clean(NormalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", fmt.Errorf("invalid path")
	}

	entries, err := workspace.ReadDir(e.workspaceDir, path)
	if err != nil {
		return "", err
	}

	var lines []string
	for _, entry := range entries {
		info, _ := entry.Info()
		if entry.IsDir() {
			lines = append(lines, fmt.Sprintf("DIR  %s/", entry.Name()))
		} else {
			lines = append(lines, fmt.Sprintf("FILE %s (%d bytes)", entry.Name(), info.Size()))
		}
	}
	if len(lines) == 0 {
		return "(empty directory)", nil
	}
	return strings.Join(lines, "\n"), nil
}

func (e *ToolExecutor) aptInstall(ctx context.Context, packages string) (string, error) {
	if e.docker.ConfinedExecution() {
		return "System packages cannot be installed into this read-only workspace. For Python libraries use install_packages with pip. For system dependencies, build an image and run it with the workspace Docker tools; mount /workspace for persistent files.", nil
	}
	cmd := fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get update -qq && apt-get install -y -qq %s", packages)
	out, err := e.docker.Exec(ctx, packageInstallCommand(cmd), 5*time.Minute)
	if err != nil {
		return fmt.Sprintf("Install failed: %v\n%s", err, out), nil
	}
	// Persist package names so they are reinstalled on container restart.
	manifestPath := filepath.Join(e.workspaceDir, ".apt-packages")
	for _, pkg := range strings.Fields(packages) {
		e.appendToManifest(manifestPath, pkg)
	}
	return fmt.Sprintf("Installed: %s\n%s", packages, out), nil
}

func (e *ToolExecutor) pipInstall(ctx context.Context, packages string) (string, error) {
	cmd := fmt.Sprintf("pip3 install --quiet %s", packages)
	out, err := e.docker.Exec(ctx, packageInstallCommand(cmd), 5*time.Minute)
	if err != nil {
		return fmt.Sprintf("pip install failed: %v\n%s", err, out), nil
	}
	// Persist package names so they are reinstalled on container restart.
	manifestPath := filepath.Join(e.workspaceDir, ".pip-packages")
	for _, pkg := range strings.Fields(packages) {
		e.appendToManifest(manifestPath, pkg)
	}
	return fmt.Sprintf("pip installed: %s\n%s", packages, out), nil
}

// appendToManifest adds an entry to a manifest file if not already present.
func (e *ToolExecutor) appendToManifest(path, entry string) {
	existing, err := e.readManagedFile(path)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return
		}
	}
	_ = e.writeManagedFile(path, append(existing, []byte(entry+"\n")...))
}

func (e *ToolExecutor) openFile(path string) (string, error) {
	path = NormalizeWorkspacePath(path)
	fullPath := filepath.Join(e.workspaceDir, path)
	f, err := workspace.Open(e.workspaceDir, path)
	if err != nil {
		if os.IsNotExist(err) {
			return e.notFoundHint(path, fullPath).Error(), nil // teach: siblings + closest name
		}
		return fmt.Sprintf("cannot open %s: %v", path, err), nil
	}
	f.Close()
	if e.onOpenFile != nil {
		e.onOpenFile(path)
	}
	return fmt.Sprintf("Opened %s in editor", path), nil
}

// ─── Web tools ────────────────────────────────────────────────────────────────

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}

func (e *ToolExecutor) addAttachment(path string) (string, []string, error) {
	path = filepath.Clean(NormalizeWorkspacePath(path))
	if strings.HasPrefix(path, "..") {
		return "", nil, fmt.Errorf("path escapes workspace")
	}

	fullPath := filepath.Join(e.workspaceDir, path)
	rel, err := filepath.Rel(e.workspaceDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", nil, fmt.Errorf("path escapes workspace")
	}
	data, err := workspace.ReadFile(e.workspaceDir, path)
	if err != nil {
		if os.IsNotExist(err) {
			return e.notFoundHint(path, fullPath).Error(), nil, nil // teach: siblings + closest name
		}
		return fmt.Sprintf("ERROR: could not read %q: %v", path, err), nil, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !imageExts[ext] {
		return fmt.Sprintf("Attached %q to your response (non-image file — download will be available).", filepath.Base(path)), nil, nil
	}
	return fmt.Sprintf("Attached %q to your response.", filepath.Base(path)), []string{base64.StdEncoding.EncodeToString(data)}, nil
}

// Preserve the install status while bounding returned logs, without a pipeline
// whose last successful command hides the package manager's failure.
func packageInstallCommand(command string) string {
	return "prism_install_log=$(mktemp) || exit 1; trap 'rm -f \"$prism_install_log\"' EXIT HUP INT TERM; (" + command + ") >\"$prism_install_log\" 2>&1; prism_install_status=$?; tail -c 8000 \"$prism_install_log\"; exit \"$prism_install_status\""
}
