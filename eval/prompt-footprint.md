# Prompt footprint baseline

Offline audit, 2026-09-22. No model request, credentials, live database or deployed
instance was used or changed. The diagnostic calls the production
`buildSystemPrompt` and `buildToolList` builders with an empty executor and a
synthetic session ID (`prompt-audit`). Guided and Standard below are the existing
`lean_prompt=false` and `lean_prompt=true` modes; Minimal is not implemented yet.

## Reproduce

From the Prism repository:

```sh
go test ./internal/agent -run '^(TestPromptFootprint|TestLeanProfile)$' -v -count=1
```

The diagnostic reports byte and Unicode character counts, sections and the 15
largest native tool schemas. It prints sizes and built-in names only, not prompt
contents or user data. The current-time string has a fixed width.

## Results

| Component | Guided bytes | Standard bytes |
| --- | ---: | ---: |
| Assembled system prompt | 31,322 | 25,397 |
| Native tool catalog (45 tools, compact pivot JSON) | 49,091 | 49,091 |
| Combined | 80,413 | 74,488 |

Standard removes 5,925 bytes: 18.9% of the system prompt, but only 7.4% of the
combined baseline. Tools represent 65.9% of the Standard baseline. The widget
section alone occupies 8,546 bytes in Standard (11,312 in Guided).

Largest tool definitions (full JSON including names, descriptions and parameter
schemas): `agent_settings` 4,359 bytes; `email` 3,648; `cron` 3,041;
`widget` 2,576; `pim_source` 2,216; `editor` 2,130; `note` 2,030.

These are **not token counts, HTTP request sizes or prices**. Provider adapters
transform the pivot representation. Their tokenizers, tool framing, image input,
reasoning and cache accounting vary. No character-to-token conversion is used.
The fixture excludes disabled-tool filtering, custom tools, MCP, Vox, personality,
user profile, history, document retrieval, skills/service indexes and live view
context. A configured instance can therefore differ in either direction.

## Existing accounting and repetition

- `internal/agent/agent.go`: each `callOllama` rebuilds the system prompt and sends
  it with the tool list and current history. The current lean flag changes prompt
  prose; it does not select shorter tool descriptions or disable retry nudges.
- That prompt can additionally contain conversation summaries, user profile,
  retrieved learnings, RAG and MCP context, skill/service indexes, current app
  selection and the global workspace overview. These are not measured here.
- `internal/ollama/client.go`: `StreamEvent` carries no token usage. Its streamed
  response struct does not retain Ollama evaluation counts.
- `internal/openai/client.go`: streamed chunks do not retain usage, and the request
  currently does not request streamed usage. `internal/anthropic/client.go` does
  not retain provider usage either. Thus input/output/cache/reasoning consumption
  cannot currently be recovered from these clients.
- `internal/server/ws.go`: the `chat_turn` quantity is an estimate from the latest
  user message and output (`(len(content)+outChars)/4`). It excludes the system
  prompt, tool definitions, replayed history and intermediate model calls. It is
  unsuitable for pricing or comparing total task cost.
- `internal/agent/executor.go`: tool-call/error events exist; the eval harness
  already counts tool calls/errors and duration and checks task outcomes.
- Widget creation/update automatically renders a preview. Depending on model
  vision support it returns an image or may call a vision model for a description.
  This is useful quality control but its image/auxiliary usage must be included
  in a future total, not attributed only to the main chat request.
- Context compaction and other auxiliary model calls also need separate attribution.
  Sending a prompt repeatedly does not imply paying its full uncached price on
  every call; current code does not measure cache hits.

## Next implementation and paid comparison

1. Add per-request usage to the common backend event contract and adapters, with
   provider-reported input/output, cache and reasoning breakdowns where available.
   Preserve unavailable fields as unknown, not zero. Avoid double-counting streamed
   cumulative usage and subsets such as cached input or reasoning output.
2. Aggregate by eval task/session, retaining request count, elapsed time, tool
   calls/errors and validation outcome. Separate auxiliary requests. Keep telemetry
   to counts/identifiers; do not record prompts, mail, documents or credentials.
3. Add Minimal as an explicit third choice. Preserve Guided and Standard. Move
   tutorials/examples to on-demand help and shorten descriptions without losing
   actual tool contracts. Check harness nudges as well as prompt prose.
4. Extend the existing `cmd/prism-eval` harness. Use identical model/reasoning
   settings, source fixtures, enabled tools and initial state; fresh sessions and
   isolated fixture paths per run. Verify its cleanup before running on a personal
   instance. Use synthetic mail and notes, not real inbox modifications.
5. Compare a weather widget, a note edit and a mail search/summary, 2–3 runs each
   per mode. Check function and visual quality, not just a successful tool return.
   Record cache conditions and provider/model identity. Report a quality/cost
   tradeoff rather than accepting a lower token count with a broken result.
6. Before paid runs, select model and budget with the owner. Use a task timeout,
   iteration cap and budget checks between requests; acknowledge that a request
   already in flight can exceed a soft cost threshold. Calculate cost only from
   verified provider rates and measured usage, with missing categories disclosed.

No paid benchmark, Minimal implementation or cost saving in dollars is claimed
by this baseline.

## Lean correction (same offline fixture)

The baseline above is retained as the before measurement. After the correction:

| Component | Guided (unchanged) | Corrected lean bytes |
| --- | ---: | ---: |
| Assembled system prompt | 31,322 | 17,589 |
| Native catalog, still 45 tools | 49,091 | 41,633 |
| Combined | 80,413 | 59,222 |

This is 20.5% fewer bytes than the original lean and 26.4% fewer than Guided.
The core lean prompt is now a compact operational reference; 22 native tool
descriptions have curated summaries rather than automatic truncation. Parameter
names/types/enums/required fields remain identical. Dynamic custom/MCP tools are
not rewritten. Disabled-tool filtering and profile override precedence remain.
Destructive-action rules remain verbatim. Automatic widget previews and harness
error recovery are not disabled by this correction.

Validation: agent package tests pass with the race detector; tests check catalog
immutability, unchanged schemas, dynamic-tool preservation, disabled-tool filtering,
profile switching, essential prompt contracts and at least 10% native catalog
reduction. No live model evaluation, billing reduction, or quality equivalence has
been established yet. No deployment or paid model request was made for this change.

## Minimal implementation

Minimal is now selectable in personal settings, group admin settings and the
agent_settings tool (`prompt_profile`). The legacy lean boolean maps to
Guided/Standard; explicit profile overrides it. No automatic model-name selection.

Measured with the new prompt_profile schema:
Guided 80,625 bytes, Standard 59,426, Minimal 47,476 (including 45 native tools).
Minimal's assembled system prompt is 5,639 bytes; tool schemas use the same compact
catalog as Standard. That's approximately 20% below Standard and 41% below Guided
for this empty fixture, not a claim about billed tokens or task quality.

Minimal preserves active-editor context and safety rules, omits automatic service
inventory, and exposes the maintained runtime reference through prism_help
`agent-runtime`. The context-dependent indexes other than service inventory are
kept to avoid hiding important capabilities or scope information.

Provider streaming counters and eval request records are implemented for the main
chat loop. See README for the exact coverage and unknown/partial-value semantics;
auxiliary requests remain outside these measurements. Tests use simulated streams
and disposable PostgreSQL; no paid model benchmark or deployment is part of this
implementation pass.
