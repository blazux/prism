# Agent eval harness

The rule for every change that touches what the model sees — the system
prompt, a tool schema, what a tool returns, the executor — is: **it may make
the agent's life simpler, never more complicated.** This harness turns that
rule into a number.

`cmd/prism-eval` replays the tasks in `tasks.json` against a running Prism,
over the same WebSocket the dashboard uses, and reports for each task:

- **success** — every check passed (files exist, widget listed, cron
  scheduled, answer contains the fact…),
- **tool calls** — how many tries it took,
- **tool errors** — how many of those tries failed,
- **duration**.

A change is accepted when, on the deployment's everyday model, the success
rate does not drop, mean tool calls do not rise by more than 15 % and mean
tool errors do not rise by more than 0.25 per task. `-baseline` enforces this
and exits 2 on regression.

## Running

Against a live instance (it needs a real model, the workspace container,
Postgres, and — for `web-*` tasks — SearXNG and outbound network):

```bash
export PRISM_URL=http://localhost:48080 PRISM_TOKEN=change-me
go run ./cmd/prism-eval                        # all tasks, once
go run ./cmd/prism-eval -only core             # tasks tagged "core"
go run ./cmd/prism-eval -only widget -v        # name/tag substring, verbose
go run ./cmd/prism-eval -runs 3 -out eval/baseline.json   # record a baseline
go run ./cmd/prism-eval -runs 3 -baseline eval/baseline.json   # gate a change
go run ./cmd/prism-eval -model qwen3.6:27b     # pin the model
```

Small local models are not deterministic: record baselines with `-runs 3`
(or more) and compare like with like — same model, same runs.

Each task runs in its own fresh session `eval-<name>` (deleted before and
after), and fixtures are created/removed through `/api/builtin`, outside the
agent. `-keep` leaves sessions and fixtures in place for inspection.

## Writing a task

```json
{
  "name": "file-write-read",
  "tags": ["files", "core"],
  "prompt": "Create a file eval/hello.txt whose content is exactly: hello",
  "setup":   [ { "tool": "write_file", "args": { "path": "…", "content": "…" } } ],
  "checks":  [
    { "tool": "read_file", "args": { "path": "eval/hello.txt" }, "contains": "hello", "no_error": true },
    { "response": true, "regex": "done|created" }
  ],
  "max_tool_calls": 3,
  "cleanup": [ { "tool": "exec_command", "args": { "command": "rm -rf eval" } } ]
}
```

- `checks` inspect either a builtin tool's result (`tool` + `args`) or the
  agent's final answer (`response: true`). Assertions: `contains`,
  `contains_all`, `contains_any`, `not_contains`, `regex` (case-insensitive,
  dot matches newline), `min_len`, `no_error`.
- `max_tool_calls` is a comfort budget: exceeding it is reported, not failed.
- Prompts are written the way a user would type them — the point is to
  measure the agent, not to prompt-engineer the task.

Prefer tasks that mirror what the deployment actually asks for. A task that
reproduces a real failure (a tool the model keeps misusing, a result that
overflowed the context) is worth more than a synthetic one.

## Where the errors come from

Every failed tool call is also audited server-side as usage kind `audit`,
item `tool_error` (with the tool name and the first line of the error), next
to `tool_denied`. The top of that table is the roadmap for agent-comfort
work: fix what actually trips the model, not what we assume does.
