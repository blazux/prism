// Package skills is a lightweight, file-backed library of reusable procedures
// ("skills") the agent can save once and recall later. Each skill is a small
// JSON file in $WORKSPACE_DIR/skills/<name>.json. The agent saves a skill when
// it works out how to do something non-trivial, and an index of every skill's
// name + when-to-use is injected into the system prompt so it knows what it can
// pull up (via the `skill` tool) on future turns.
package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	WhenToUse   string `json:"when_to_use"`
	Body        string `json:"body"`
}

type Manager struct {
	dir string
}

func NewManager(dir string) *Manager { return &Manager{dir: dir} }

// sanitize maps a skill name to a safe, stable filename stem.
func sanitize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (m *Manager) path(name string) string {
	return filepath.Join(m.dir, sanitize(name)+".json")
}

// Save writes (or overwrites) a skill. Name and body are required.
func (m *Manager) Save(s Skill) error {
	if sanitize(s.Name) == "" {
		return fmt.Errorf("skill name is required")
	}
	if strings.TrimSpace(s.Body) == "" {
		return fmt.Errorf("skill body is required")
	}
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path(s.Name), b, 0644)
}

// Get returns a single skill by name.
func (m *Manager) Get(name string) (Skill, bool, error) {
	b, err := os.ReadFile(m.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, false, nil
		}
		return Skill{}, false, err
	}
	var s Skill
	if err := json.Unmarshal(b, &s); err != nil {
		return Skill{}, false, err
	}
	return s, true, nil
}

// List returns every saved skill, sorted by name.
func (m *Manager) List() ([]Skill, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		var s Skill
		if json.Unmarshal(b, &s) == nil && s.Name != "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete removes a skill. Missing skills are not an error.
func (m *Manager) Delete(name string) error {
	err := os.Remove(m.path(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
