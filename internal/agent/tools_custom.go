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
	name, hdrErr := extractToolName(code)
	if name == "" {
		if hdrErr != nil {
			return "", fmt.Errorf("# TOOL: header found but unusable — %v. The header must be ONE line of valid JSON with a \"name\" field; fix it and try again", hdrErr)
		}
		return "", fmt.Errorf("no # TOOL: {...} header found (must be one line, valid JSON, with a \"name\" field) — fix the header and try again")
	}
	base := toolFilename(name)
	if base == ".py" {
		return "", fmt.Errorf("tool name %q has no usable characters for a filename", name)
	}

	if e.customMgr.IsProtectedFilename(base) {
		return "", fmt.Errorf("a tool named %q is shipped with Prism and cannot be overwritten — pick a different name", name)
	}

	// A custom tool must not collide with a built-in or an MCP tool's name.
	// Execute() matches built-ins first, then custom, then MCP — so a colliding
	// name SILENTLY SHADOWS the other tool: a built-in name leaves the new custom
	// tool dead (unreachable), and an MCP name makes the *MCP* tool unreachable
	// (the agent registers "get_ticket" and the MCP "get_ticket" vanishes with no
	// warning). Refuse at creation, like the protected-tool guard, so the shadow
	// never happens. Re-registering an existing custom tool of the same name is
	// still fine (that is how the agent edits one) — only names owned by a
	// built-in or an MCP server are blocked.
	for _, t := range ToolDefinitions {
		if t.Function.Name == name {
			return "", fmt.Errorf("%q is a built-in Prism tool — a custom tool with that name would never run (built-ins take priority). Pick a different name.", name)
		}
	}
	if e.mcpMgr != nil {
		if _, ok := e.mcpSessionFor(name); ok {
			return "", fmt.Errorf("%q is already provided by a connected MCP server — a custom tool with that name would hide the MCP tool and make it unreachable. Pick a different name.", name)
		}
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
	// Personal secrets plus the group's shared tier (personal wins on a name
	// collision); reserved integration credentials never reach the env.
	for name, value := range e.secretsEnv(ctx) {
		env[name] = value
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
	// Model-context cap only: a programmatic caller (prismTool / cron) gets the
	// whole output, as /api/tool/ always did — see SetRawResults.
	if !e.rawResults && len(out) > 8000 {
		out = out[:4000] + "\n...[truncated]...\n" + out[len(out)-4000:]
	}
	return out, nil
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// extractToolName parses the "# TOOL: {...}" header line and returns the
// declared tool name. When a header line exists but can't be used, the error
// says exactly WHY (the JSON parse error, or the missing "name" field) — a
// bare "no valid header" sent the model hunting for accents and quotes when
// the real defect was one missing closing brace (measured on xslog_sip:
// two identical retries fixing red herrings).
func extractToolName(code string) (string, error) {
	var hdrErr error
	for _, line := range strings.SplitN(code, "\n", 30) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# TOOL:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "# TOOL:"))
		var m struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			// "unexpected end of JSON input" means the {...} never closes on
			// this line: almost always an unbalanced brace (count them), since
			// a header broken across lines wouldn't start with "# TOOL:" past
			// the first. Spell that out — it's the actual failure mode seen.
			if strings.Contains(err.Error(), "unexpected end of JSON input") {
				hdrErr = fmt.Errorf("the header's JSON never closes — count your braces: every { needs its } on that same line (parse error: %v)", err)
			} else {
				hdrErr = fmt.Errorf("invalid JSON in header: %v", err)
			}
			continue // a later line may carry the real header
		}
		if m.Name == "" {
			hdrErr = fmt.Errorf("header JSON parsed but has no \"name\" field")
			continue
		}
		return m.Name, nil
	}
	return "", hdrErr
}

// ─── RAG tool ─────────────────────────────────────────────────────────────────
