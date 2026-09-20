package email

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ManageFolderWithRules is the UI/agent entry point. Keep stored destinations
// aligned with IMAP while excluding concurrent rule edits and executions.
func (c Config) ManageFolderWithRules(ctx context.Context, store RuleStore, action, name, target string) (string, error) {
	if action == "create_folder" {
		return c.ManageFolderResolved(action, name, target)
	}
	rulesMu.Lock()
	defer rulesMu.Unlock()
	rules, err := LoadRules(ctx, store)
	if err != nil {
		return "", err
	}
	rows, err := c.Folders()
	if err != nil {
		return "", err
	}
	delimiter := ""
	for _, f := range rows {
		if f.Name == name {
			delimiter = f.Delimiter
			break
		}
	}
	affected := []int{}
	for i, r := range rules {
		if r.Action == "move" && (r.Target == name || (delimiter != "" && strings.HasPrefix(r.Target, name+delimiter))) {
			if action == "delete_folder" {
				return "", fmt.Errorf("folder is used by rule %q; change its destination or delete the rule first (paused rules also keep their destination)", r.Name)
			}
			affected = append(affected, i)
		}
	}
	resolved, err := c.ManageFolderResolved(action, name, target)
	if err != nil {
		return "", err
	}
	if action != "rename_folder" || len(affected) == 0 {
		return resolved, nil
	}
	for _, i := range affected {
		rules[i].Target = resolved + strings.TrimPrefix(rules[i].Target, name)
	}
	data, err := json.Marshal(rules)
	if err == nil {
		err = store.SetConfig(ctx, RulesKey, string(data))
	}
	if err != nil {
		// The rule store and IMAP cannot share a transaction. Undo the rename if
		// persistence fails, and report both failures if the provider rejects undo.
		if undoErr := c.manageFolderExact("rename_folder", resolved, name); undoErr != nil {
			return "", fmt.Errorf("folder renamed to %q but rules could not be saved (%v); undo failed (%v): update affected rule destinations before running them", resolved, err, undoErr)
		}
		return "", fmt.Errorf("rules could not be saved; folder rename was undone: %w", err)
	}
	return resolved, nil
}
