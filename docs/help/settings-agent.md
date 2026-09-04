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

## Lean prompt (frontier models)
Prism's default system prompt carries step-by-step guardrails that small local
models need. A capable model (Claude, GPT-5, large hosted models) wastes turns
on them — tick **Lean prompt** to drop that scaffolding. Leave it off for small
Ollama models. Safety rules stay on either way.

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
reasoning**, **Lean prompt** and **Reasoning effort**, then **Save agent**.
These override the personal settings whenever that agent answers — in the
Room or on the group's Webex bot.
