# Prompt behavior review — 24 September 2026

Changes apply to the common Prism engine (local and hosted), without changing
configuration, tool schemas or tool permissions. Nothing was deployed by this task.

- Common decision policy: resolve a necessary fact or requested action with a tool;
  answer directly otherwise; stop for essential user input and after completion.
- Grounding is relevant and proportional, rather than requiring a fresh source for
  every assertion. Existing usable evidence may be reused.
- Live RAG collection guidance now requires relevance to the actual request. The
  same constant is used by the production server and the model probe.
- Standard/minimal have no automatic announcement nudge. Guided retains one
  bounded reminder, suppressing questions and recognizable waiting language.
  It is system guidance, not a fabricated user instruction to continue.
- Memory instructions no longer demand routine post-deployment entries or storing
  default credentials. Secret values never belong in lessons or skills.
- Removed the conflicting blanket widget session-URL instruction: prismTool
  already handles session/auth. Existing runtime recipes remain available.

## Results

The scripted backend emits six representative French/English clarifications for
all three profiles. With the old nudge condition restored via a Go source overlay,
all 18 cases incorrectly invoke the model three times. With the correction they
invoke it once and stop. Tests exercise Agent.Chat, not just the regex. Separate
coverage retains the bounded guided announcement fallback and confirms it is
absent for standard/minimal.

The configured OpenAI-compatible provider alias `flash-next` was called with eight
synthetic scenarios in all three profiles, before and after the changes. Each
profile passed 8/8 both before and after, with three appropriate tool calls and
zero tools on the five direct-answer/clarification scenarios. No tool was executed.
The final prompt received a further complete 24-case run, also 24/24.

This demonstrates the stopping bug fix and no first-decision regression in this
sample. It does **not** demonstrate a general improvement over Cline, validate
long task completion, or reproduce the user's exact conversation. No tester
conversation, private collection or model settings were changed.

Full `go test ./...` passed. Agent/server tests also passed with `-race`.
Integration tests requiring explicitly configured services retain their normal
skip behavior. Reproduction instructions are in [README.md](README.md).
Machine-readable totals are in [prompt-behavior-2026-09-24.json](prompt-behavior-2026-09-24.json).

The change targets behavior, not token savings. Offline system + native schema
sizes (bytes, not tokens; excludes history, MCP and user context):

| Profile | Before | After |
| --- | ---: | ---: |
| Guided | 81,079 | 81,399 |
| Standard | 59,883 | 60,126 |
| Minimal | 47,813 | 49,248 |

Limits: the guided clarification detector is a conservative language heuristic,
not a semantic guarantee; automatic per-turn learning retrieval is unchanged.
Browser-independent execution and subagents remain separate work.
