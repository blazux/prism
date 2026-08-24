package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/customtools"
)

// slugify reduces a free-text string to lowercase [a-z0-9_-] characters, for
// deriving a machine-safe identifier (a filename, a widget id) from something
// a model would otherwise have to invent and keep in sync by hand alongside
// the human-readable original — see toolFilename and addUIPlugin's id.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// toolFilename derives the on-disk script name from the tool's own declared
// name, so there is exactly one name to get right instead of two independent
// strings (a filename the caller picks, plus the "name" in the # TOOL: header)
// that are free to disagree — the exact shape of trap that cost real time
// elsewhere in this codebase (add_attachment's path handling, MCP scope).
// list_tools/Get() already key by this declared name, never by filename, so
// the file's name was always just a storage detail, never something callers
// needed to choose themselves.
func toolFilename(name string) string { return slugify(name) + ".py" }

func (e *ToolExecutor) registerTool(code string) (string, error) {
	if e.customMgr == nil {
		return "", fmt.Errorf("custom tools not configured")
	}
	name := extractToolName(code)
	if name == "" {
		return "", fmt.Errorf("no valid # TOOL: {...} header found (must be one line, valid JSON, with a \"name\" field) — fix the header and try again")
	}
	base := toolFilename(name)
	if base == ".py" {
		return "", fmt.Errorf("tool name %q has no usable characters for a filename", name)
	}

	if e.customMgr.IsProtectedFilename(base) {
		return "", fmt.Errorf("a tool named %q is shipped with Prism and cannot be overwritten — pick a different name", name)
	}

	path := filepath.Join(e.customMgr.Dir(), base)
	if err := os.WriteFile(path, []byte(code), 0644); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	if e.onToolsReload != nil {
		e.onToolsReload()
	}

	source := filepath.Join(filepath.Base(e.customMgr.Dir()), base)
	return fmt.Sprintf("Tool '%s' registered — it now appears in the admin panel and is immediately callable. Source: %s (inspect or edit later with read_file/write_file/edit).", name, source), nil
}

func (e *ToolExecutor) listTools() (string, error) {
	if e.customMgr == nil {
		return "Custom tools not configured.", nil
	}
	e.customMgr.Reload()
	tools := e.customMgr.All()
	if len(tools) == 0 {
		return "No custom tools registered yet. Use register_tool to create one.", nil
	}
	// The tools live in customMgr.Dir() (an absolute host path); the file tools
	// (read_file/write_file/edit) take workspace-relative paths, so surface the
	// dir's basename — e.g. "agent_tools" — which they accept as-is via
	// NormalizeWorkspacePath. Derived, not hardcoded, so it can't drift from
	// where server.go actually puts the dir.
	toolsRel := filepath.Base(e.customMgr.Dir())
	var sb strings.Builder
	fmt.Fprintf(&sb, "Custom tools (%d):\n", len(tools))
	for _, t := range tools {
		if t.Protected {
			// Shipped with Prism (e.g. pcap.py, re-materialized from the binary).
			// The agent may call it, but write_file has NO overwrite guard — so
			// don't hand out an editable source path that would invite clobbering
			// a built-in tool. Label it instead.
			fmt.Fprintf(&sb, "  - %s (%s) [shipped with Prism — do not edit]: %s\n", t.Name, t.Filename, t.Description)
			continue
		}
		fmt.Fprintf(&sb, "  - %s (%s): %s\n", t.Name, t.Filename, t.Description)
		fmt.Fprintf(&sb, "      source: %s/%s — inspect or edit with read_file, write_file, edit\n", toolsRel, t.Filename)
	}
	return sb.String(), nil
}

func (e *ToolExecutor) execCustomTool(ctx context.Context, tool *customtools.Tool, rawArgs json.RawMessage) (string, error) {
	session := e.sessionID
	if session == "" {
		session = "default"
	}
	env := map[string]string{
		"PRISM_SESSION": session,
		"PRISM_URL":     "http://prism-server:8080",
		"PRISM_TOKEN":   e.prismToken,
	}
	if e.memStore != nil {
		if secrets, err := e.memStore.ScopedSecrets(ctx, e.SecretsScope()); err == nil {
			for name, value := range secrets {
				// Skip built-in integration credentials (email, OAuth, Telegram, …):
				// they share this scope with user-created keys but must not leak into
				// the shared workspace env. Everything else is a request_secret key.
				if isReservedSecretName(name) {
					continue
				}
				env[toEnvVarName(name)] = value
			}
		}
	}
	// Pass the JSON payload via stdin to avoid shell argument-length limits (ARG_MAX).
	// The one-liner shim injects stdin into sys.argv[1] so tools are unchanged.
	cmd := fmt.Sprintf(
		"cd /workspace && python3 -c 'import sys,runpy;sys.argv=[sys.argv[1],open(\"/dev/stdin\").read()];runpy.run_path(sys.argv[0],run_name=\"__main__\")' %s",
		shellEscape("/workspace/agent_tools/"+tool.Filename),
	)
	out, err := e.docker.ExecWithStdin(ctx, cmd, []byte(rawArgs), 2*time.Minute, env)
	if err != nil {
		return fmt.Sprintf("ERROR: %v\nOutput: %s", err, out), nil
	}
	if len(out) > 8000 {
		out = out[:4000] + "\n...[truncated]...\n" + out[len(out)-4000:]
	}
	return out, nil
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func extractToolName(code string) string {
	for _, line := range strings.SplitN(code, "\n", 30) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# TOOL:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "# TOOL:"))
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(raw), &m) == nil {
			return m.Name
		}
	}
	return ""
}

// ─── RAG tool ─────────────────────────────────────────────────────────────────
