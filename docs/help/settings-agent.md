# Settings → Agent

Everything about *how your agent behaves* lives in **Settings → Agent**. The
model itself is picked in the chat header (the model dropdown next to the
theme selector), not here.

## Agent identity
- **Agent avatar** — an image shown next to the agent's replies in chat
  (**Agent avatar** to upload, **Remove** to clear).
- **Name** — e.g. "Prism", "Jarvis". Used everywhere the agent speaks.
- **Default personality** — the base persona applied in every workspace: tone,
  language, rules, what to focus on. Click **Save**; it takes effect on the
  next message.

You can also just tell the agent "from now on, answer in French and keep it
short" — it can rewrite its own personality.

## Per-workspace adaptations
Each workspace gets a collapsible card at the bottom of the tab. Open one, type
extra instructions, **Save**. They are layered *on top of* the default
personality for that workspace only; leave it blank to use the default as-is.

## Turn budget
One message can trigger many model calls (every tool use is one). **Max
iterations per turn** caps that: 10–500, blank = the default (75). If a long
task stops with *"Iteration limit reached (N model calls this turn). Raise it in
Settings › Agent, or send a follow-up to continue."*, either raise the cap or
simply send a follow-up — the agent continues from where it stopped.

Independently of this cap, a call repeated with identical arguments and an
identical result three times in a row is stopped early as a loop.

## Extended reasoning
Lets thinking models (Qwen3, DeepSeek-R1, gpt-oss…) reason before answering.
While it thinks, the chat shows a *Thinking…* indicator, and the finished
reasoning is folded into a collapsible **💭 Reasoning** block above the reply.
Turn it off for faster, cheaper replies. It applies to the Ollama and
OpenAI-compatible backends only (Claude models never use it here), and it is
always off on phone calls.

## Prompt profile
Choose **Guided**, **Standard** or **Minimal** in Settings → Agent, then click
**Save** below the turn settings. Group admins use Admin → Shared agent instead.
Changes apply on the next message; use a fresh conversation for a fair comparison.

- **Guided**: detailed guidance for small models (the original default).
- **Standard**: compact operational instructions and native tool descriptions
  (the former Lean prompt).
- **Minimal**: essential contracts and safety policy; detailed runtime guidance is
  available through `prism_help` with topic `agent-runtime`. Designed for capable
  models, without inferring capabilities from their names.

Existing Lean off/on settings map to Guided/Standard; nobody is switched to
Minimal automatically. All tools and parameter schemas remain available, including
custom/MCP tools. Server permissions, destructive-action rules, automatic widget
previews and error recovery remain active. Minimal keeps the open editor/app
context; it omits the automatic deployed-services inventory (use docker_manage ps).

The agent can read its profile with `agent_settings action=get` and set it with
`agent_settings action=set prompt_profile=minimal` (or guided/standard). The legacy
`lean_prompt` boolean still maps to Guided/Standard; an explicit prompt_profile
wins if both are supplied. Shared-agent settings belong in the group admin UI.

Minimal reduces the fixed input size, not necessarily the bill by the same ratio:
history, tool results, reasoning, images, caching and additional calls also matter.

## Reasoning effort
How much a thinking model reasons before answering (only when extended
reasoning is on). Choose **Server default**, **low**, **medium**, **high** or
**xhigh**. The accepted values depend on the model:

- gpt-oss: low / medium / high
- Qwen3.8-Flash-Next: low / medium / xhigh

An unsupported value is not caught when you save — the backend refuses it on
the *next message*, and the error shows up in chat. Just pick another one.
**Server default** means the deployment's `OPENAI_REASONING_EFFORT` (medium).

Click the second **Save** (under Reasoning effort) to store the turn budget,
reasoning and prompt options — they also take effect on the next message.

## The shared group agent
In a shared deployment, every group also has a **shared agent** with the same
knobs, set by a group admin in the **Admin console → Shared agent** pane:
**Group**, **Name** (the one members @mention in the Room), **Avatar**,
**Model**, **System prompt**, **Max iterations per turn**, **Extended
reasoning**, **Prompt profile** and **Reasoning effort**, then **Save agent**.
These override the personal settings whenever that agent answers — in the
Room or on the group's Webex bot.

## Asking the agent

The agent can change these settings itself: "call yourself Shodan", "raise your turn budget to 150", "turn reasoning off", "use low reasoning effort", "from now on, everywhere, answer in French and keep it short" (the default personality). "Only in this workspace, …" changes the per-workspace adaptation instead. It confirms the new values; they apply from the next message. A group's shared agent cannot do this for itself — its settings are in the admin console.

## Personal timezone
In **Settings → Profile**, choose an IANA timezone such as `America/Martinique`
or `Europe/Paris`, or click **Use browser timezone**, then **Save**. Changes
apply without a restart. Leave it empty to use the deployment's `TZ` default.
The agent can read/change it with `agent_settings` (`timezone`).

This controls the agent's clock, dates supplied without an explicit offset,
and new or rescheduled cron jobs. Each job keeps its saved timezone when your
profile changes. Existing legacy jobs continue using the workspace's timezone.
Calendar/task forms and display follow the browser timezone; the active editor
reports that timezone to the agent. Explicit ISO offsets always take precedence.

Zoned cron jobs require Python 3 with zoneinfo/tzdata in the workspace; Prism
checks availability before saving. The UI shows the saved zone. At DST changes,
a nonexistent local minute is skipped and a repeated minute runs twice.
