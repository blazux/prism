package email

import (
	"context"
	"errors"
	"testing"
)

type failingRuleStore struct{ ruleMemory }

func (f failingRuleStore) SetConfig(context.Context, string, string) error {
	return errors.New("storage offline")
}

func TestFolderRenameMaintainsRules(t *testing.T) {
	c := testMailbox(t, true)
	ctx := context.Background()
	store := ruleMemory{}
	for _, name := range []string{"Projects", "Projects/Sub", "ProjectsOther"} {
		if err := c.ManageFolder("create_folder", name, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []Rule{
		{Name: "parent", Field: "subject", Contains: "invoice", Action: "move", Target: "Projects", Enabled: true},
		{Name: "child", Field: "from", Contains: "example", Action: "move", Target: "Projects/Sub", Enabled: false},
		{Name: "other", Field: "subject", Contains: "other", Action: "move", Target: "ProjectsOther", Enabled: true},
	} {
		if err := SaveRule(ctx, store, r); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := c.ManageFolderWithRules(ctx, store, "rename_folder", "Projects", "Work")
	if err != nil || resolved != "Work" {
		t.Fatal(resolved, err)
	}
	rules, err := LoadRules(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if rules[0].Target != "Work" || rules[1].Target != "Work/Sub" || rules[1].Enabled || rules[2].Target != "ProjectsOther" {
		t.Fatalf("%+v", rules)
	}
	// imapmemserver.Rename only moves the exact map entry, unlike IMAP's
	// hierarchical rename. Complete that server-side effect for the fixture.
	if err := c.manageFolderExact("rename_folder", "Projects/Sub", "Work/Sub"); err != nil {
		t.Fatal(err)
	}
	appendMail(t, c, "INBOX", "invoice")
	res, err := c.RunRules(ctx, rules, false)
	if err != nil || res[0].Applied != 1 || res[0].Error != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err = c.ManageFolderWithRules(ctx, store, "delete_folder", "Work/Sub", ""); err == nil {
		t.Fatal("deleted destination of paused rule")
	}
	if err = DeleteRule(ctx, store, "child"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ManageFolderWithRules(ctx, store, "delete_folder", "Work/Sub", ""); err != nil {
		t.Fatal(err)
	}
}
func TestFolderRenamePersistenceFailureUndoesIMAP(t *testing.T) {
	c := testMailbox(t, true)
	ctx := context.Background()
	store := ruleMemory{}
	if err := c.ManageFolder("create_folder", "Projects", ""); err != nil {
		t.Fatal(err)
	}
	if err := SaveRule(ctx, store, Rule{Name: "move", Field: "subject", Contains: "invoice", Action: "move", Target: "Projects", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ManageFolderWithRules(ctx, failingRuleStore{store}, "rename_folder", "Projects", "Work"); err == nil {
		t.Fatal("reported success without saved rules")
	}
	folders, err := c.Folders()
	if err != nil {
		t.Fatal(err)
	}
	old := false
	for _, f := range folders {
		if f.Name == "Work" {
			t.Fatal("rename not undone")
		}
		if f.Name == "Projects" {
			old = true
		}
	}
	if !old {
		t.Fatal("original folder missing")
	}
	rules, _ := LoadRules(ctx, store)
	if rules[0].Target != "Projects" {
		t.Fatal(rules)
	}
	if _, err := c.ManageFolderWithRules(ctx, store, "rename_folder", "Projects", "Archive"); err == nil {
		t.Fatal("existing target accepted")
	}
	rules, _ = LoadRules(ctx, store)
	if rules[0].Target != "Projects" {
		t.Fatal("failed rename changed rule")
	}
}
