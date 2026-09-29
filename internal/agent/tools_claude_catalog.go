package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"prism/internal/ollama"
)

// Claude-only, opt-in experiment. The selection lives in the turn context: a
// later user turn starts with the same small catalog, not the previous loadout.
type claudeCatalogKey struct{}

type claudeCatalog struct {
	loaded map[string]bool
}

func withClaudeCatalog(ctx context.Context, model string, guarded bool) context.Context {
	if os.Getenv("PRISM_CLAUDE_LAZY_TOOLS") != "1" ||
		!strings.HasPrefix(model, "anthropic::claude-") || guarded {
		return ctx
	}
	return context.WithValue(ctx, claudeCatalogKey{}, &claudeCatalog{loaded: make(map[string]bool)})
}

func catalogFromContext(ctx context.Context) *claudeCatalog {
	value, _ := ctx.Value(claudeCatalogKey{}).(*claudeCatalog)
	return value
}

var claudeCatalogTool = ollama.Tool{Type: "function", Function: ollama.ToolFunction{
	Name:        "tool_catalog",
	Description: "Enable Prism tools for this turn. If you know exact names, call load directly with all needed names; list is only for discovering unknown names and short purposes. Loaded tools appear on the NEXT model call. Only tools allowed in this session appear. No user data is changed.",
	Parameters: ollama.ToolParameters{Type: "object", Properties: map[string]ollama.ToolProperty{
		"action": {Type: "string", Enum: []string{"list", "load"}},
		"names":  {Type: "array", Description: "Exact names to enable with action=load.", Items: &ollama.ToolProperty{Type: "string"}},
	}, Required: []string{"action"}},
}}

var claudeCoreTools = map[string]bool{
	"exec_command": true,
	"read_file":    true,
	"write_file":   true,
	"widget":       true,
	"cron":         true,
	"prism_help":   true,
}

// Base tools stay first and cacheable; loaded schemas follow. The ordinary
// filtered list already excludes hidden, disabled and duplicate definitions.
func (c *claudeCatalog) selectTools(all []ollama.Tool) ([]ollama.Tool, int) {
	base := make([]ollama.Tool, 0, len(claudeCoreTools)+1)
	loaded := make([]ollama.Tool, 0, len(c.loaded))
	for _, tool := range all {
		name := tool.Function.Name
		if claudeCoreTools[name] {
			base = append(base, tool)
		} else if c.loaded[name] {
			loaded = append(loaded, tool)
		}
	}
	base = append(base, claudeCatalogTool)
	return append(base, loaded...), len(base)
}

func (c *claudeCatalog) execute(raw json.RawMessage, available []ollama.Tool) (string, error) {
	var args struct {
		Action string   `json:"action"`
		Names  []string `json:"names"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid catalog arguments: %w", err)
	}
	byName := make(map[string]ollama.Tool, len(available))
	for _, tool := range available {
		byName[tool.Function.Name] = tool
	}
	switch args.Action {
	case "list":
		lines := make([]string, 0, len(available))
		for _, tool := range available {
			if claudeCoreTools[tool.Function.Name] {
				continue
			}
			description := strings.TrimSpace(tool.Function.Description)
			if i := strings.IndexByte(description, '.'); i >= 0 {
				description = description[:i+1]
			}
			if len(description) > 110 {
				description = description[:107] + "..."
			}
			lines = append(lines, tool.Function.Name+": "+description)
		}
		sort.Strings(lines)
		return "Available tools (load exact names before calling):\n" + strings.Join(lines, "\n"), nil
	case "load":
		if len(args.Names) == 0 {
			return "", fmt.Errorf("names are required for load")
		}
		var loaded, unknown []string
		for _, name := range args.Names {
			if _, ok := byName[name]; !ok {
				unknown = append(unknown, name)
				continue
			}
			if !claudeCoreTools[name] && !c.loaded[name] {
				c.loaded[name] = true
				loaded = append(loaded, name)
			}
		}
		if len(unknown) > 0 {
			return fmt.Sprintf("Loaded: %s. Unavailable: %s. Use list for exact available names.", strings.Join(loaded, ", "), strings.Join(unknown, ", ")), nil
		}
		return "Loaded: " + strings.Join(loaded, ", ") + ". These tools are available on the next model call.", nil
	default:
		return "", fmt.Errorf("action must be list or load")
	}
}
