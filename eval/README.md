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
- **duration**,
- **estimated Claude chat cost** — provider tokens priced at published API
  rates, including 5-minute cache reads/writes; never an execution limit.

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

The optional live scenario in `weather-live.json` checks the full public API
→ local JSON → widget → scheduled refresh path. It calls Open-Meteo and a
real Claude model, so run it only on a disposable local instance with test
credits; the fixture cleans up its widget, cron and files afterward:

```bash
go run ./cmd/prism-eval -tasks eval/weather-live.json -model anthropic::claude-sonnet-5
```

To measure the upper bound of a smaller tool catalog on the same task, run
`eval/weather-live-targeted-tools.json` with the same model. It exposes only
`exec_command`, `widget`, `cron`, `read_file`, and `write_file` through the
existing `allowed_tools` fixture field. Keep model, prompt, instance and cache
conditions comparable. This is an **oracle** experiment: the useful tools are
selected in advance, not discovered by Claude. It does not test Anthropic's
server-side tool search or account for its search-call cost and latency.

```bash
go run ./cmd/prism-eval -tasks eval/weather-live-targeted-tools.json -model anthropic::claude-sonnet-5-5 -runs 3
```

Anthropic's native tool search has a separate, opt-in local probe:
`PRISM_ANTHROPIC_TOOL_SEARCH=1` (Sonnet 5.5 only). It is **off by default and
not production-ready**. On 2026-09-29, keeping the five tools above visible
and deferring the rest passed the weather task 4/4; the three warm-cache runs
averaged about $0.052 versus $0.069 for the full-catalog control. This task
did not actually search for a deferred tool. A separate `list_files` discovery
fixture failed 3/3 with Anthropic `refusal` (category `cyber`), while the
same prompt with native search off passed 1/1. The probe also uses a complete
non-streaming response and keeps provider search blocks only in live memory.
Do not enable it broadly from the weather result alone.

```bash
go run ./cmd/prism-eval -tasks eval/tool-search-discovery.json -model anthropic::claude-sonnet-5-5
```

Prism's own Claude-only catalog is a separate opt-in probe:
`PRISM_CLAUDE_LAZY_TOOLS=1` (mutually exclusive with native tool search).
It starts with `exec_command`, `read_file`, `write_file`, `widget`, `cron`,
`prism_help`, and `tool_catalog`; `tool_catalog` lists/loads the remaining
permitted tools for that turn. Other providers keep the full catalog. On
2026-09-29 the weather fixture passed 3/3 at ~$0.182 total versus ~$0.298
with the full Minimal catalog (three runs each); first-request input fell
from ~24.4k to ~5.9k tokens. The `list_files` discovery fixture passed 2/2,
loading it in one extra model step. Small samples and different cache states
mean this is evidence for further evaluation, not a production rollout or a
guaranteed percentage saving. The flag is off by default.


## CalDAV, against a disposable server

The PIM providers have live tests that only run when a throwaway CalDAV server
is pointed at. They cover what no unit test can: whether the server expands a
recurring series, and whether a read-modify-write really preserves what it sent
back. **Never point these at a real account.**

```bash
docker run -d --name prism-caldav-test -p 127.0.0.1:5232:5232 \
  -v "$PWD/caldav-test/config:/config:ro" tomsquest/docker-radicale
curl -s -X MKCALENDAR -u eval:eval http://127.0.0.1:5232/eval/agenda/

export PRISM_TEST_CALDAV_URL=http://127.0.0.1:5232 \
       PRISM_TEST_CALDAV_USER=eval PRISM_TEST_CALDAV_PASS=eval
go test ./internal/calendar/ ./internal/tasks/ -run Live -v
```

Without those variables the tests skip, so the everyday `go test ./...` stays
self-contained.

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
- Optional `allowed_tools` lists the native tools the agent may see for a
  controlled catalog-size experiment. The harness disables other native tools
  for that turn through the existing dashboard protocol; custom/MCP tools stay
  available. This is an oracle upper bound, not automatic tool selection.
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

## Offline prompt size audit

See [prompt-footprint.md](prompt-footprint.md) for the reproducible Guided/Standard
baseline, current usage-counter limitations and the protocol for comparing Minimal.
The diagnostic makes no model calls and does not require a running instance.

## Comparing prompt profiles and provider usage

Select Guided/Standard/Minimal in the instance's Agent settings before each batch.
Do not change the setting during a run. Each result's `model_requests` now keeps
one `main_chat` or `subagent` record per model call: actual profile, model,
duration, system/tool bytes, history sizes (not content), message count,
completion status and provider-reported `usage` if available. This includes
intermediate chat/tool-loop calls and delegated calls, not only the final
response. Subagent records also appear in the parent task's Admin → Usage trace.

`input_tokens` includes cached input. `cache_read_tokens`/`cache_write_tokens` are
breakdowns; do not add them again. `reasoning_tokens`, when reported, is part of
`output_tokens`, not extra output. Missing fields (or `usage: null`) mean unknown,
not zero. Interrupted streams may have partial or missing usage. Snapshot counts
are cumulative per request; only the last reported snapshot is recorded.

The report also shows priced/unpriced request counts, input/output/cache tokens
and the estimated cost for supported Claude models. Unknown prices or missing
provider counts remain unpriced, not zero. Prices are kept in
`internal/usagecost/pricing.go` and must be reviewed when models or rates change.

These records **do not cover auxiliary vision captions, deep-research model calls,
compaction or embeddings**, and are not a complete bill. The legacy admin chat-turn
estimate is separate; never add it to provider counts. In PostgreSQL, records use
`usage_events.kind=model_request`, qty=1 and meta.measurement; no prompts, tool
results or credentials are added to these telemetry records. New records include a
content-free task ID shared with subagents; older records can only be grouped by
session. Admin → Usage shows these aggregates, with partial-cost caveats.

Adapters follow [OpenAI's streaming usage contract](https://developers.openai.com/api/reference/resources/chat)
and [Anthropic's cumulative streaming usage](https://platform.claude.com/docs/en/build-with-claude/streaming),
with its [cache input normalization](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).
An OpenAI-compatible endpoint explicitly rejecting stream_options is retried without
that parameter; consumption remains unknown if it does not report it.

## Tool relevance and clarification regression

`go test ./internal/agent -run 'TestClarificationEndsActualAgentLoop|TestAnnouncementFallbackProfiles|TestPromptDecisionAcrossProfiles'`
checks stopping behavior through the actual agent loop with a scripted backend.
A necessary clarification must end the turn, including when preceded by an
announcement. Standard/minimal never receive an announcement reminder; guided
allows at most one conservative reminder, supplied as harness/system guidance.

The opt-in first-decision probe uses the real prompt builder, native tool catalog
and OpenAI-compatible adapter with synthetic context. It never executes the
returned tools or accesses personal data. Provider calls may incur charges.

```bash
# Set these explicitly for a test provider; do not commit credentials.
export PRISM_PROBE_URL=http://localhost:8000/v1
export PRISM_PROBE_MODEL=my-model
export PRISM_PROBE_OUTPUT=/tmp/prism-prompt-decisions.json
# PRISM_PROBE_KEY is optional for providers requiring authentication.
go test ./internal/agent -run '^TestPromptBehaviorProbe$' -count=1 -v -timeout=12m
```

Eight scenarios run in each of the three profiles: general explanation, rewriting,
supplied facts, clarification, thanks, note creation, unread mail and relevant RAG.
The first five expect no tool; the others expect the appropriate tool. Profiles
run concurrently (up to three requests). The report includes responses, tool names
and latency. This checks the first decision only, not complete task execution or
long-conversation quality. The full-stack evaluator remains necessary for those.
