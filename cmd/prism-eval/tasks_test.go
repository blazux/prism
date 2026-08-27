package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"prism/internal/agent"
)

// eval/tasks.json is data the harness trusts blindly; a typo in a tool name
// would surface as "every run fails" against a live instance, minutes later.
// Validate it at build time instead: well-formed, unique names, every tool
// referenced in setup/checks/cleanup is a real built-in (or one the task
// itself creates), and every check asserts something.
func TestTasksFileIsValid(t *testing.T) {
	raw, err := os.ReadFile("../../eval/tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var tasks []Task
	if err := json.Unmarshal(raw, &tasks); err != nil {
		t.Fatalf("tasks.json: %v", err)
	}
	if len(tasks) < 10 {
		t.Fatalf("only %d tasks — the harness needs breadth to mean anything", len(tasks))
	}

	builtin := map[string]bool{}
	for _, def := range agent.ToolDefinitions {
		builtin[def.Function.Name] = true
	}
	// Tools a task creates itself and then verifies through /api/builtin.
	created := map[string]bool{"eval_double": true}

	nameRe := regexp.MustCompile(`^[a-z0-9-]+$`)
	seen := map[string]bool{}
	for _, task := range tasks {
		if !nameRe.MatchString(task.Name) {
			t.Errorf("%q: name must be a slug (it becomes the session id)", task.Name)
		}
		if seen[task.Name] {
			t.Errorf("%q: duplicate task name", task.Name)
		}
		seen[task.Name] = true
		if task.Prompt == "" {
			t.Errorf("%q: empty prompt", task.Name)
		}
		if len(task.Checks) == 0 {
			t.Errorf("%q: no checks — a task that cannot fail measures nothing", task.Name)
		}
		for _, tc := range append(append([]ToolCall{}, task.Setup...), task.Cleanup...) {
			if !builtin[tc.Tool] && !created[tc.Tool] {
				t.Errorf("%q: unknown tool %q in setup/cleanup", task.Name, tc.Tool)
			}
		}
		for i, ck := range task.Checks {
			if ck.Response && ck.Tool != "" {
				t.Errorf("%q check %d: inspect either the response or a tool, not both", task.Name, i)
			}
			if !ck.Response {
				if ck.Tool == "" {
					t.Errorf("%q check %d: no tool and not a response check", task.Name, i)
				} else if !builtin[ck.Tool] && !created[ck.Tool] {
					t.Errorf("%q check %d: unknown tool %q", task.Name, i, ck.Tool)
				}
			}
			if ck.Contains == "" && len(ck.ContainsAll) == 0 && len(ck.ContainsAny) == 0 &&
				ck.NotContains == "" && ck.Regex == "" && ck.MinLen == 0 && !ck.NoError {
				t.Errorf("%q check %d: asserts nothing", task.Name, i)
			}
			if ck.Regex != "" {
				if _, err := regexp.Compile("(?is)" + ck.Regex); err != nil {
					t.Errorf("%q check %d: bad regex: %v", task.Name, i, err)
				}
			}
		}
	}
}

func TestAssert(t *testing.T) {
	if f := assert(Check{Contains: "a", Regex: "^b", MinLen: 5}, "abc"); len(f) != 2 {
		t.Errorf("expected regex + min_len failures, got %v", f)
	}
	if f := assert(Check{ContainsAny: []string{"x", "b"}, NotContains: "z"}, "abc"); len(f) != 0 {
		t.Errorf("expected pass, got %v", f)
	}
}
