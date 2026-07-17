package customtools

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"prism/internal/ollama"
)

type Tool struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	// WhenToUse / Usage are optional richer metadata folded into the description
	// the model sees, so it picks the tool by intent (when + how) rather than by
	// guessing from the name. Both are backward-compatible (empty = old behavior).
	WhenToUse   string                `json:"when_to_use"`
	Usage       string                `json:"usage"`
	Parameters  ollama.ToolParameters `json:"parameters"`
	Filename    string                `json:"filename"`
}

// llmDescription composes the description sent to the model from the base
// description plus the optional when-to-use and usage hints.
func (t Tool) llmDescription() string {
	d := t.Description
	if t.WhenToUse != "" {
		d += "\n\nWhen to use: " + t.WhenToUse
	}
	if t.Usage != "" {
		d += "\n\nUsage: " + t.Usage
	}
	return d
}

type Manager struct {
	dir   string
	mu    sync.RWMutex
	tools []Tool
}

func NewManager(dir string) *Manager {
	os.MkdirAll(dir, 0755)
	m := &Manager{dir: dir}
	m.Reload()
	return m
}

func (m *Manager) Dir() string { return m.dir }

func (m *Manager) Reload() {
	tools := m.discover()
	m.mu.Lock()
	m.tools = tools
	m.mu.Unlock()
}

func (m *Manager) All() []Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Tool, len(m.tools))
	copy(out, m.tools)
	return out
}

func (m *Manager) Get(name string) *Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.tools {
		if m.tools[i].Name == name {
			t := m.tools[i]
			return &t
		}
	}
	return nil
}

func (m *Manager) ToOllamaTools() []ollama.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ollama.Tool, 0, len(m.tools))
	for _, t := range m.tools {
		out = append(out, ollama.Tool{
			Type: "function",
			Function: ollama.ToolFunction{
				Name:        t.Name,
				Description: t.llmDescription(),
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

func (m *Manager) discover() []Tool {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil
	}
	var tools []Tool
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".py" {
			continue
		}
		path := filepath.Join(m.dir, e.Name())
		if tool, ok := parseToolMeta(path, e.Name()); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// parseToolMeta scans the first 30 lines for a "# TOOL: {...}" comment.
func parseToolMeta(path, filename string) (Tool, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Tool{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 0; i < 30 && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "# TOOL:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "# TOOL:"))
		var meta struct {
			Name        string                `json:"name"`
			Description string                `json:"description"`
			WhenToUse   string                `json:"when_to_use"`
			Usage       string                `json:"usage"`
			Parameters  ollama.ToolParameters `json:"parameters"`
		}
		if err := json.Unmarshal([]byte(raw), &meta); err != nil || meta.Name == "" {
			continue
		}
		return Tool{
			Name:        meta.Name,
			Description: meta.Description,
			WhenToUse:   meta.WhenToUse,
			Usage:       meta.Usage,
			Parameters:  meta.Parameters,
			Filename:    filename,
		}, true
	}
	return Tool{}, false
}
