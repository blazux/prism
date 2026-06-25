package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"prism/internal/skills"
)

func (e *ToolExecutor) skillsMgr() *skills.Manager {
	return skills.NewManager(filepath.Join(e.workspaceDir, "skills"))
}

// SkillsIndex returns a compact "name — when to use" list of every saved skill,
// injected into the system prompt so the agent knows what it can recall. Empty
// when there are no skills.
func (e *ToolExecutor) SkillsIndex() string {
	list, err := e.skillsMgr().List()
	if err != nil || len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Saved skills\n")
	sb.WriteString("Reusable procedures you saved earlier. When one fits the task, call `skill action=get name=<name>` to load its full steps before acting.\n")
	for _, s := range list {
		when := s.WhenToUse
		if when == "" {
			when = s.Description
		}
		fmt.Fprintf(&sb, "- **%s** — %s\n", s.Name, when)
	}
	return sb.String()
}

// skillTool implements the `skill` tool: add / list / get / delete.
func (e *ToolExecutor) skillTool(action, name, description, whenToUse, body string) (string, error) {
	mgr := e.skillsMgr()
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add", "save", "update":
		if err := mgr.Save(skills.Skill{Name: name, Description: description, WhenToUse: whenToUse, Body: body}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Skill %q saved.", name), nil

	case "list", "":
		list, err := mgr.List()
		if err != nil {
			return "", err
		}
		if len(list) == 0 {
			return "No skills saved yet.", nil
		}
		var sb strings.Builder
		for _, s := range list {
			when := s.WhenToUse
			if when == "" {
				when = s.Description
			}
			fmt.Fprintf(&sb, "- %s — %s\n", s.Name, when)
		}
		return sb.String(), nil

	case "get", "read", "show":
		s, ok, err := mgr.Get(name)
		if err != nil {
			return "", err
		}
		if !ok {
			return fmt.Sprintf("No skill named %q.", name), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "# %s\n", s.Name)
		if s.Description != "" {
			fmt.Fprintf(&sb, "%s\n", s.Description)
		}
		if s.WhenToUse != "" {
			fmt.Fprintf(&sb, "\nWhen to use: %s\n", s.WhenToUse)
		}
		fmt.Fprintf(&sb, "\n%s\n", s.Body)
		return sb.String(), nil

	case "delete", "remove":
		if err := mgr.Delete(name); err != nil {
			return "", err
		}
		return fmt.Sprintf("Skill %q deleted.", name), nil

	default:
		return "", fmt.Errorf("skill: unknown action %q (expected add, list, get, delete)", action)
	}
}
