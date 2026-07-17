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

func (e *ToolExecutor) registerTool(filename, code string) (string, error) {
	if e.customMgr == nil {
		return "", fmt.Errorf("custom tools not configured")
	}
	if !strings.HasSuffix(filename, ".py") {
		return "", fmt.Errorf("filename must end in .py")
	}
	base := filepath.Base(filename)
	if strings.HasPrefix(base, ".") {
		return "", fmt.Errorf("invalid filename")
	}

	path := filepath.Join(e.customMgr.Dir(), base)
	if err := os.WriteFile(path, []byte(code), 0644); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	if e.onToolsReload != nil {
		e.onToolsReload()
	}

	name := extractToolName(code)
	if name == "" {
		return fmt.Sprintf("Script written to agent_tools/%s but no valid # TOOL: header found — check your header format.", base), nil
	}
	return fmt.Sprintf("Tool '%s' registered from %s — it now appears in the admin panel and is immediately callable.", name, base), nil
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
	var sb strings.Builder
	fmt.Fprintf(&sb, "Custom tools (%d):\n", len(tools))
	for _, t := range tools {
		fmt.Fprintf(&sb, "  - %s (%s): %s\n", t.Name, t.Filename, t.Description)
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
		if secrets, err := e.memStore.GetAllSecrets(ctx); err == nil {
			for name, value := range secrets {
				// Skip user-scoped credentials ("u<id>:email_password", …): they are
				// personal (email, OAuth, Telegram) and must not leak into the shared
				// workspace env. Only team-shared, user-created secrets are exported.
				if strings.Contains(name, ":") {
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
