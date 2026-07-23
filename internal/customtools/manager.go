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
	Name        string `json:"name"`
	Description string `json:"description"`
	// WhenToUse / Usage are optional richer metadata folded into the description
	// the model sees, so it picks the tool by intent (when + how) rather than by
	// guessing from the name. Both are backward-compatible (empty = old behavior).
	WhenToUse  string                `json:"when_to_use"`
	Usage      string                `json:"usage"`
	Parameters ollama.ToolParameters `json:"parameters"`
	Filename   string                `json:"filename"`
	// Protected marks a tool the agent may call and reload, but must never
	// delete or overwrite — set via "protected":true in the tool's own
	// "# TOOL: {...}" header. For tools shipped WITH Prism (e.g. the embedded
	// pcap reader, agent_tools/pcap.py, re-materialized from the binary at
	// boot) rather than created by the agent itself via register_tool. A
	// self-declared flag on the file rather than external config, so any
	// future embedded tool is protected automatically with no code changes
	// elsewhere. See IsProtected.
	Protected bool `json:"protected"`
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

// IsProtectedFilename reports whether the tool CURRENTLY on disk under this
// filename (e.g. "pcap.py") is marked protected — used by delete_file and
// register_tool's overwrite path, both of which only have a filename/path to
// go on, not the tool's declared name. Deliberately checks the DISCOVERED
// (already-parsed-from-disk) set, never a caller-supplied claim, so a new
// file that merely calls itself "pcap" without actually being on disk yet
// can't bypass the check.
func (m *Manager) IsProtectedFilename(filename string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.tools {
		if t.Filename == filename {
			return t.Protected
		}
	}
	return false
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
			Protected   bool                  `json:"protected"`
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
			Protected:   meta.Protected,
		}, true
	}
	return Tool{}, false
}
