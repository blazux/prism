package customtools

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"prism/internal/ollama"
	"prism/internal/workspace"
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
	// File stamps include invalid scripts too, so fixing a header in place is
	// noticed even though editing a file does not change the directory mtime.
	files    map[string]fileStamp
	reloadMu sync.Mutex
}

type fileStamp struct {
	Size     int64
	Modified int64
}

func NewManager(dir string) *Manager {
	os.MkdirAll(dir, 0755)
	return LoadManager(dir)
}

// LoadManager discovers existing tools without creating deployment directories.
func LoadManager(dir string) *Manager {
	m := &Manager{dir: dir}
	m.Reload()
	return m
}

func (m *Manager) Dir() string { return m.dir }

func (m *Manager) Reload() {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	// Capture stamps before parsing. A file changed during discovery then
	// differs at the next read instead of being recorded as already current.
	files := m.fileStamps()
	tools := m.discover()
	m.mu.Lock()
	m.tools = tools
	m.files = files
	m.mu.Unlock()
}

func (m *Manager) fileStamps() map[string]fileStamp {
	files := map[string]fileStamp{}
	entries, err := workspace.ReadDir(filepath.Dir(m.dir), filepath.Base(m.dir))
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".py" {
			continue
		}
		// Open through the workspace boundary, including for symlink targets.
		f, err := workspace.Open(filepath.Dir(m.dir), filepath.Join(filepath.Base(m.dir), entry.Name()))
		if err != nil {
			continue
		}
		info, err := f.Stat()
		f.Close()
		if err == nil && info.Mode().IsRegular() {
			files[entry.Name()] = fileStamp{Size: info.Size(), Modified: info.ModTime().UnixNano()}
		}
	}
	return files
}

// Refresh additions, removals and edits made through shell/terminal as well as
// through native file tools. Only changed files cause metadata to be reparsed.
func (m *Manager) refreshIfStale() {
	files := m.fileStamps()
	m.mu.RLock()
	stale := len(files) != len(m.files)
	if !stale {
		for name, stamp := range files {
			if old, ok := m.files[name]; !ok || old != stamp {
				stale = true
				break
			}
		}
	}
	m.mu.RUnlock()
	if stale {
		m.Reload()
	}
}

func (m *Manager) All() []Tool {
	m.refreshIfStale()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Tool, len(m.tools))
	copy(out, m.tools)
	return out
}

func (m *Manager) Get(name string) *Tool {
	m.refreshIfStale()
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
	m.refreshIfStale()
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
	m.refreshIfStale()
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
	entries, err := workspace.ReadDir(filepath.Dir(m.dir), filepath.Base(m.dir))
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
	f, err := workspace.Open(filepath.Dir(filepath.Dir(path)), filepath.Join(filepath.Base(filepath.Dir(path)), filename))
	if err != nil {
		return Tool{}, false
	}
	defer f.Close()

	tool, err := ParseHeader(f)
	tool.Filename = filename
	return tool, err == nil
}

// ErrNoHeader distinguishes a missing header from a malformed one.
var ErrNoHeader = errors.New("no # TOOL: {...} header found in the first 30 lines")

// ParseHeader is shared by registration and discovery: an accepted script must
// expose the same complete metadata when it is subsequently loaded from disk.
func ParseHeader(source io.Reader) (Tool, error) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var headerErr error
	for i := 0; i < 30 && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "# TOOL:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "# TOOL:"))
		var meta Tool
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			if strings.Contains(err.Error(), "unexpected end of JSON input") {
				headerErr = fmt.Errorf("the header's JSON never closes — count your braces: every { needs its } on that same line (parse error: %v)", err)
			} else {
				headerErr = fmt.Errorf("invalid JSON in header: %w", err)
			}
			continue
		}
		headerErr = nil
		if strings.TrimSpace(meta.Name) == "" {
			headerErr = fmt.Errorf("header JSON parsed but has no \"name\" field")
			continue
		}
		if meta.Parameters.Type == "" {
			meta.Parameters.Type = "object" // legacy no-argument tools omit parameters
		}
		if meta.Parameters.Type != "object" {
			headerErr = fmt.Errorf("tool parameters must have type \"object\"")
			continue
		}
		for _, name := range meta.Parameters.Required {
			if _, ok := meta.Parameters.Properties[name]; !ok {
				headerErr = fmt.Errorf("required parameter %q has no property definition", name)
				break
			}
		}
		if headerErr == nil {
			return meta, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return Tool{}, fmt.Errorf("read tool header: %w", err)
	}
	if headerErr != nil {
		return Tool{}, headerErr
	}
	return Tool{}, ErrNoHeader
}
