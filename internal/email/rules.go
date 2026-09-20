package email

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/emersion/go-imap/v2"
)

const RulesKey = "email_rules"

// One condition and one action per rule keeps the UI and model contract small.
// Rules run on INBOX in order, without a model call, including existing mail.
type Rule struct {
	Name     string `json:"name"`
	Field    string `json:"field"`
	Contains string `json:"contains"`
	Action   string `json:"action"`
	Target   string `json:"target,omitempty"`
	Enabled  bool   `json:"enabled"`
}
type RuleStore interface {
	GetConfig(context.Context, string) (string, bool, error)
	SetConfig(context.Context, string, string) error
}

// Serializes edits and runs, including the background worker and manual apply.
// The lock is process-local: Prism's server is a single process per deployment.
var rulesMu sync.Mutex

func LoadRules(ctx context.Context, store RuleStore) ([]Rule, error) {
	raw, ok, err := store.GetConfig(ctx, RulesKey)
	if err != nil {
		return nil, err
	}
	rules := []Rule{}
	if ok && raw != "" {
		if err = json.Unmarshal([]byte(raw), &rules); err != nil {
			return nil, fmt.Errorf("cannot read saved email rules: %w", err)
		}
	}
	return rules, nil
}
func (r Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("rule name is required")
	}
	if r.Field != "from" && r.Field != "to" && r.Field != "subject" {
		return fmt.Errorf("rule_field must be from, to or subject")
	}
	if strings.TrimSpace(r.Contains) == "" {
		return fmt.Errorf("rule_contains is required")
	}
	if r.Action != "move" && r.Action != "mark_read" {
		return fmt.Errorf("rule_action must be move or mark_read")
	}
	if r.Action == "move" {
		if err := folderName(r.Target); err != nil {
			return err
		}
		if strings.EqualFold(r.Target, "INBOX") {
			return fmt.Errorf("a move rule needs a destination other than INBOX")
		}
	}
	return nil
}
func SaveRule(ctx context.Context, store RuleStore, rule Rule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	rulesMu.Lock()
	defer rulesMu.Unlock()
	rules, err := LoadRules(ctx, store)
	if err != nil {
		return err
	}
	found := false
	for i, r := range rules {
		if r.Name == rule.Name {
			rules[i] = rule
			found = true
			break
		}
	}
	if !found {
		if len(rules) >= 50 {
			return fmt.Errorf("at most 50 rules")
		}
		rules = append(rules, rule)
	}
	b, _ := json.Marshal(rules)
	return store.SetConfig(ctx, RulesKey, string(b))
}
func DeleteRule(ctx context.Context, store RuleStore, name string) error {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	rules, err := LoadRules(ctx, store)
	if err != nil {
		return err
	}
	for i, r := range rules {
		if r.Name == name {
			rules = append(rules[:i], rules[i+1:]...)
			b, _ := json.Marshal(rules)
			return store.SetConfig(ctx, RulesKey, string(b))
		}
	}
	return fmt.Errorf("rule %q not found; use rules to list names", name)
}

type RuleResult struct {
	Name     string    `json:"name"`
	Matched  int       `json:"matched"`
	Applied  int       `json:"applied"`
	Messages []Message `json:"messages,omitempty"`
	Error    string    `json:"error,omitempty"`
}

func ruleCriteria(r Rule) *imap.SearchCriteria {
	c := &imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: r.Field, Value: r.Contains}}}
	if r.Action == "mark_read" {
		c.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	return c
}
func (c Config) RunRules(ctx context.Context, rules []Rule, preview bool) ([]RuleResult, error) {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	cl, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	// Closing the connection interrupts a long IMAP command when the caller stops.
	stop := context.AfterFunc(ctx, func() { cl.Close() })
	defer stop()
	if _, err = cl.Select("INBOX", &imap.SelectOptions{ReadOnly: preview}).Wait(); err != nil {
		return nil, err
	}
	c.Folder = "INBOX"
	results := []RuleResult{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if err = r.Validate(); err != nil {
			return results, err
		}
		rr := RuleResult{Name: r.Name}
		data, err := cl.UIDSearch(ruleCriteria(r), nil).Wait()
		if err != nil {
			rr.Error = err.Error()
			results = append(results, rr)
			continue
		}
		uids := data.AllUIDs()
		rr.Matched = len(uids)
		// Drain a bounded batch each pass. Moving removes matches, marking read
		// excludes them next time, so subsequent passes make forward progress.
		if len(uids) > 100 {
			uids = uids[:100]
		}
		if preview {
			rr.Messages, err = c.fetchByUIDs(cl, uids)
			if err != nil {
				rr.Error = err.Error()
			}
		} else {
			for _, uid := range uids {
				if ctx.Err() != nil {
					return results, ctx.Err()
				}
				if r.Action == "move" {
					err = moveUID(cl, uid, r.Target)
				} else {
					err = cl.Store(imap.UIDSetNum(uid), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
				}
				if err != nil {
					rr.Error = err.Error()
					break
				}
				rr.Applied++
			}
		}
		results = append(results, rr)
	}
	return results, nil
}
