# <img src="logo.svg" alt="Prism logo" height="22"> PRISM

**Programmable Responsive Interface for Smart Models**

*Also known as: Probably Runs Interesting Stuff Magically*

Yes, another AI dashboard. Except this one runs entirely on your own hardware — no cloud, no API keys, no data leaving your machine — and has the slightly unsettling property of being able to modify its own environment.

<img src="gui.png" width="700" alt="PRISM dashboard">

<details>
<summary>More screenshots</summary>

<img src="chat.png" width="700" alt="Chat with the agent">
<img src="settings.png" width="700" alt="Agent settings">

</details>

---

## The idea

This started as frustration with tools like [OpenClaw](https://openclaw.ai) and [Hermes](https://hermes-agent.nousresearch.com) — both promising, both Node.js (make of that what you will), and both operating on the same fundamental assumption: the agent lives somewhere in the background, has direct access to your disk, and you talk to it through a chat box that it has absolutely no influence over. Which is fine, until you realize that a truly useful agent should be able to shape its own workspace, not just occupy it.

PRISM is built around a different premise: the agent has actual agency over its environment. It runs code in a sandboxed workspace (not on your host machine — you're welcome), builds interactive widgets and pins them directly to the dashboard it lives in, schedules tasks, searches the web, queries a private knowledge base, and — if it needs a capability it doesn't have — it can define new tools and use them immediately, or connect a remote MCP server and configure it entirely on its own.

You ask it to monitor something, it builds a widget. You ask it to set up a GitHub integration, it connects the MCP server, fetches its auth token, and gets to work. You ask it to remember a document, it indexes it with embeddings and will bring it up when relevant.

Is this a great idea? Probably. Does it make you slightly nervous? It should. That's how you know it's doing something real.

---

## Quick start

Three things and you're in: a model server, a `.env`, and `docker compose up`.

**1. Have a model server.** The default is [Ollama](https://ollama.com), local or on another box, with a chat model and an embedding model pulled:

```bash
ollama pull qwen3.6:27b          # chat — anything with tool calling works
ollama pull qwen3-embedding:8b   # embeddings, for the knowledge base
```

Not on Ollama? vLLM, LM Studio, llama.cpp or Claude all work too — see [Choosing your backend](#choosing-your-backend) below and come back.

**2. Configure.**

```bash
git clone https://github.com/blazux/prism
cd prism
cp .env.example .env
```

Open `.env` and set the three lines that matter — everything else works out of the box:

```dotenv
OLLAMA_URL=http://host-gateway:11434   # host-gateway = the machine running Docker
OLLAMA_MODEL=qwen3.6:27b
EMBED_MODEL=qwen3-embedding:8b
```

Set `TZ` to your timezone while you're there, and `PRISM_TOKEN` if anyone but you can reach the port.

**3. Run.**

```bash
docker compose up -d
```

Open [http://localhost:48080](http://localhost:48080), type your `PRISM_TOKEN` if you set one, and say hi. The first message takes a moment — the model is loading. Something off? [First run & troubleshooting](#first-run--troubleshooting) has the usual suspects.

> Docker + Docker Compose are the only host requirements. PostgreSQL (pgvector), SearXNG and Traefik are bundled in the compose file — nothing else to install.

---

## Choosing your backend

Prism speaks three dialects. Pick one as the default with `LLM_BACKEND`, or configure several — every model from every configured backend shows up in the same picker, and each message goes to the server that holds the model you chose.

| | **Ollama** (default) | **OpenAI-compatible** | **Anthropic / Claude** |
|---|---|---|---|
| Talks to | Ollama | vLLM, SGLang, TGI, LM Studio, llama.cpp, LiteLLM, OpenRouter… | api.anthropic.com |
| Chat + tools | ✅ | ✅ | ✅ |
| Embeddings (RAG) | ✅ | ✅ if the server exposes `/v1/embeddings` — else `EMBED_BACKEND=ollama` | ❌ — RAG stays on Ollama automatically |
| Vision (looks at widgets) | ✅ with a vision model | ✅ with a vision model | ✅ |
| Reasoning on/off toggle | ✅ | ✅ (`enable_thinking`) | — never requested |
| Runs on your hardware | ✅ | ✅ (or not, your call) | ❌ |
| Costs | electricity | electricity | **tokens** — see below |

### Ollama — the everyday setup

```dotenv
OLLAMA_URL=http://host-gateway:11434
OLLAMA_MODEL=qwen3.6:27b
EMBED_MODEL=qwen3-embedding:8b
```

That's the whole thing. `host-gateway` resolves to the Docker host; use a plain URL for a remote box.

### OpenAI-compatible — your own inference server

```dotenv
LLM_BACKEND=openai
OPENAI_BASE_URL=http://host-gateway:8000/v1   # the /v1 root
OPENAI_MODEL=qwen                             # the server's --served-model-name
# OPENAI_API_KEY=                             # only if the server asks for one
EMBED_MODEL=qwen3-embedding:8b
# EMBED_BACKEND=ollama                        # if the server has no /v1/embeddings
```

Chat-only servers are common (a 120B on vLLM rarely bothers with embeddings) — keep `OLLAMA_URL` set and `EMBED_BACKEND=ollama` and RAG carries on quietly over there. Heavy reasoners (gpt-oss and friends) get `reasoning_effort=medium` by default so they don't spend the whole token budget thinking and forget to answer; `OPENAI_REASONING_EFFORT=low|high|none` overrides it.

### Anthropic — Claude, for when local isn't enough

```dotenv
ANTHROPIC_API_KEY=sk-ant-api03-…
# ANTHROPIC_MODEL=claude-sonnet-5     # the default if you make it LLM_BACKEND=anthropic
```

You don't have to make Claude the default. Set the key alongside your local backend and its models simply join the picker: local model for daily driving, Claude for the hard stuff, switched per message. Only the messages where you pick Claude are billed. Embeddings stay on Ollama (Anthropic serves none), so keep `OLLAMA_URL` and `EMBED_MODEL` set.

> **On the subject of billing.** Prism is an *agent*, not a chatbot. A single "check my server and fix the disk alert" is fifteen model calls, each one carrying the system prompt, the tool schemas, the conversation so far and whatever `ls -la` returned. Now multiply by a widget-building session and a cron job that runs every hour. Claude is excellent at this, and it will bill you for every token of that excellence — with the same cheerful thoroughness it applies to everything else. Set a spending limit on console.anthropic.com *before* the first "make me a dashboard", not after the invoice. A local model is slower and dumber and costs you nothing per iteration; that trade-off is the whole reason the picker exists.

<details>
<summary><b>Why a Claude Pro/Max subscription won't work (and this is deliberate)</b></summary>

The OAuth token the `claude` CLI stores does authenticate, and plain chat runs on it — but Anthropic classifies a tool-bearing request from anything that isn't Claude Code as a third-party app and bills it against *extra usage* rather than plan limits, so the agent loop is refused. The refusal is intermittent and no model or setting avoids it ([hermes-agent#31668](https://github.com/NousResearch/hermes-agent/issues/31668) is the same wall from the other side, closed with no fix). Prism is an agent, so a brain that drops tool calls at random is worse than no brain: it was implemented, measured, and taken out rather than shipped as a trap. Paste that token into `ANTHROPIC_API_KEY` and Prism tells you why it won't work instead of letting Anthropic answer `401`.

</details>

### All of them at once

```dotenv
LLM_BACKEND=ollama                          # the default brain
OLLAMA_URL=http://host-gateway:11434
OLLAMA_MODEL=qwen3.6:27b
EMBED_MODEL=qwen3-embedding:8b
OPENAI_BASE_URL=http://gpu-box:8000/v1      # the heavyweight
OPENAI_MODEL=qwen3-235b
ANTHROPIC_API_KEY=sk-ant-api03-…            # the expensive one
```

One picker, three servers, per-message choice. Webhooks and rooms can each pin their own model too.

---

## Stack

| Layer | Tech |
|---|---|
| Backend | Go |
| Frontend | Vanilla JS, custom free-floating window manager |
| LLM | Ollama — or any OpenAI-compatible server (vLLM, SGLang, TGI, …), or Claude |
| Embeddings | Ollama, or the same OpenAI-compatible server |
| Vector store | PostgreSQL + pgvector |
| Web search | SearXNG |
| Service routing | Traefik |
| Messaging | Telegram / Slack / Webex bridge |
| Integrations | Email (IMAP/SMTP), CalDAV, Todoist, Google, Microsoft, Obsidian/Logseq |
| Modes | Personal (single user) or shared (accounts, groups, rooms) — one flag |
| Runtime | Docker / Docker Compose |

---

## Embedded services

The agent can spin up full Docker containers on demand — Uptime Kuma, Jupyter, Grafana, ComfyUI, whatever has an image. Each service gets its own subdomain and is embedded directly in the dashboard as a widget iframe.

When the agent calls `docker_run`, Prism:
1. Auto-allocates a host port from the range `20000–20999`
2. Adds Traefik labels so the container is routed via `http://<name>.localhost/`
3. Strips `X-Frame-Options` so the service can be embedded as an iframe from the dashboard

**Why `*.localhost`?** Chrome and Firefox resolve `anything.localhost` to `127.0.0.1` natively — no DNS server, no `/etc/hosts` entries, no configuration. The service runs at the root path `/`, so SPAs, Vue Router, socket.io and anything else work exactly as if you'd accessed it directly, with none of the subdirectory-proxy headaches.

**Why Traefik?** It watches the Docker socket and configures routes automatically when containers appear or disappear. The agent doesn't need to know about it — `docker_run` handles everything.

To embed a service the agent has started, a widget uses a plain iframe:

```html
<iframe src="http://uptime-kuma.localhost/" style="width:100%;height:100%;border:none"></iframe>
```

---

## The apps (yes, it has a personal life now)

An assistant that can't see your actual life is just a fancy autocomplete. So Prism plugs into the boring-but-essential stuff, and the agent drives all of it from chat — or you can, from the command palette (**Ctrl+K**):

- **Email** — reads and sends through your IMAP/SMTP mailbox (a ProtonMail Bridge container is included, because of course you use ProtonMail). Tag and triage, search, reply, send **with attachments**, get your inbox summarized, or turn an email into a task or a calendar event. Pair it with cron and it'll DM you a morning digest over Telegram. It is a communication *aid*, not a mail client — there are deliberately no folders to obsess over.
- **Calendar & Tasks** — events and to-dos in Prism's own database out of the box; connect **CalDAV** (Apple iCloud, Nextcloud, Fastmail) or **Todoist** for tasks, and **Google** or **Microsoft** for your calendar. "Add lunch with Sam Friday at noon" does what you'd hope.
- **Notes** — Markdown with `[[wikilinks]]`, a split editor with an AI toolbar, and an "Add to knowledge" button that shoves a note straight into a RAG collection. Lives in Prism's database, or in your existing **Obsidian / Logseq vault** (just a folder of `.md` files).
- **Terminal** — a real, full TTY into the agent's workspace container. Toggle with **Ctrl+Enter**. `vim`, `htop`, colours, package installs — all work. For when you trust the agent right up until you don't.
- **Reach it from anywhere** — **Telegram**, **Slack** and **Webex** bridges, so you can bother the agent from your phone while pretending to work.

Connecting an account is a one-time OAuth click or an app password in **Settings**. Prism never hosts a shared OAuth app — you create your own, because your calendar is nobody's business but yours.

---

## Webhooks: let anything talk to the agent

Point any system that can make an HTTP call at a webhook URL and its payload
becomes an agent turn, wrapped in a prompt you write. Grafana fires an alert, a
form gets submitted, CI goes red, a sensor trips — the agent receives it with its
whole toolset and does whatever the prompt says: triage it, chart it, file a
task, wake you on Telegram.

```bash
curl -X POST "https://your-prism/api/webhook/<id>?token=<token>" \
     -d '{"alert":"disk","host":"gx10","pct":97}'
```

Configure them in **Settings → Webhooks**: the prompt (with `{{content}}` where
the payload goes), which chat session it runs in, an optional model, and whether
to push the answer to Telegram/Slack/Webex. Calls return `202` immediately and
the agent works in the background — tick a box if you'd rather wait for its
reply in the response.

> The URL carries its own token and sits outside the dashboard login, because
> senders are machines with no account. Anyone holding it can make your agent
> run — treat it like a password.

---

## Personal, or shared

Same binary, one flag. Decide which you are before the first `up`:

| | **Personal** (default) | **Shared** (`MULTI_USER=1`) |
|---|---|---|
| Who it's for | You. Maybe your household on the NAS. | A team, a lab, an association — several people, one Prism |
| Login | `PRISM_TOKEN`, or nothing at all | Accounts: email + password, signup page |
| Who's admin | Whoever reaches the port | The first person to sign up; they approve everyone after |
| Agents | Your agent, your workspaces | Everyone gets their own agent **plus** a shared agent per group |
| Integrations (mail, calendar, OAuth…) | Global | Scoped per user — your mailbox is yours |
| Where to configure | Settings | Settings for yourself, **Admin console** for the deployment |

> Personal mode without `PRISM_TOKEN` means *anyone who can reach the port is you*. Fine on a laptop; set the token the moment the box has a LAN address.

### How shared mode works

**Accounts.** The first signup becomes the global admin, auto-approved. Every later signup lands as *pending* and can't log in until an admin approves it in **Admin → Users** (admins are notified). No open registration by surprise.

**Groups** are the unit of collaboration. The global admin creates them and adds members; a member can be promoted to *group admin* for that group. Each group has:

- a **shared agent** — its own name, avatar, model, system prompt and turn budget, configured by a group admin. It lives in the group's **Room**, a chat where members talk to each other and to the agent by @mention, and it's the one that answers on the group's **Webex** bot;
- a **knowledge base** and **MCP servers** shared with the whole group — the shared agent and every member's personal agent can use them, members see them read-only;
- **group secrets** for those MCP servers and tools;
- a **tool policy**: the global admin sets the ceiling for every tool (*open to members* / *admins only* / *disabled*), a group admin can only tighten it for their group. Everything is open by default — it's a trusted deployment, not a hostile one.

**What stays personal.** Each member keeps their own agent, personality, workspaces, chat history, and their own integrations. Group things are additive: your agent gains the group's knowledge base, it doesn't lose yours.

### Switching

> **Turning `MULTI_USER` on is a one-way door.** At the first start in shared mode, Prism migrates the deployment's existing config keys and secrets into the scope of the first admin — so *you* must be that first signup, or your mail and calendar settings end up belonging to whoever beat you to it. Single-user mode won't find them afterwards. Starting from a fresh volume is the clean way; flipping an existing install works as long as you sign up first.

---

## It remembers, and occasionally learns

Prism keeps a memory of who you are and how you work, records lessons from problems it stumbled through, and saves multi-step jobs as reusable **skills** — so the second time you ask, it doesn't reinvent the wheel. Long conversations get summarized automatically, and it can full-text search everything you've ever discussed, which means it (mostly) stops asking you to repeat yourself. Secrets you hand it are stored **AES-256-GCM encrypted**, not in a plaintext file it'll cheerfully commit to Git.

---

## Configuration

Everything is an environment variable, set in `.env` (the annotated [`.env.example`](.env.example) follows the same sections). Docker Compose reads it on `up`.

### Required

| Variable | What | Example |
|---|---|---|
| `OLLAMA_URL` | Your Ollama instance | `http://host-gateway:11434` |
| `OLLAMA_MODEL` | Chat model (pulled) | `qwen3.6:27b` |
| `EMBED_MODEL` | Embedding model (pulled) | `qwen3-embedding:8b` |

### Backends

| Variable | What | Default |
|---|---|---|
| `LLM_BACKEND` | Default chat backend: `ollama`, `openai` or `anthropic` | `ollama` |
| `OPENAI_BASE_URL` | `/v1` root of an OpenAI-compatible server | — |
| `OPENAI_MODEL` | Its chat model (`--served-model-name`) | — |
| `OPENAI_API_KEY` | Bearer token, if the server wants one | — |
| `OPENAI_REASONING_EFFORT` | `low` / `medium` / `high` / `none` for reasoning models | `medium` |
| `ANTHROPIC_API_KEY` | API key from console.anthropic.com (not a subscription token) | — |
| `ANTHROPIC_MODEL` | Default Claude model | `claude-sonnet-5` |
| `ANTHROPIC_BASE_URL` | Only for a proxy/gateway | `https://api.anthropic.com` |
| `EMBED_BACKEND` | Where embeddings run: `ollama` or `openai`; empty = follow `LLM_BACKEND` (Anthropic → Ollama) | — |
| `CHAT_VISION` | `false` if the chat model is text-only — widget previews are captioned instead | `true` |
| `VISION_MODEL` | Model used for that captioning | the chat model |

### Everything else

| Variable | What | Default |
|---|---|---|
| `PRISM_TOKEN` | Login token for the dashboard; unset = no login | — |
| `MULTI_USER` | `1` for accounts, groups, rooms and an admin console ([one-way door](#personal-or-shared)) | off |
| `TZ` | IANA timezone for cron and timestamps (`Europe/Paris`) | `UTC` |
| `SEARXNG_URL` | SearXNG for web search; remove to disable | `http://searxng:8080` |
| `POSTGRES_URL` | PostgreSQL connection string | bundled service |
| `WORKSPACE` | `gpu` for the CUDA workspace image (~20 GB) | ubuntu base |
| `SERVICE_PORT_START` / `_END` | Host port range for agent-launched containers | `20000–20999` |
| `AGENT_CONTAINER`, `WORKSPACE_DIR`, `PLUGIN_DIR` | Internal Docker plumbing — leave alone | set |

Things that aren't env vars — the agent's name and personality, its **turn budget** (max iterations per message, reasoning on/off), integrations, webhooks — live in **Settings** and change without a restart.

---

## First run & troubleshooting

- **It can't reach Ollama.** From inside Docker, `localhost` is the container, not your machine. Use `host-gateway` (the compose file maps it) or the host's LAN IP. `docker compose logs -f prism-server` shows what it tried.
- **"model not found".** The name must match `ollama list` exactly, tag included — `qwen3.6:27b`, not `qwen3.6`.
- **Replies come back empty or cut off on a reasoning model.** It spent the whole budget thinking. Lower `OPENAI_REASONING_EFFORT`, or switch reasoning off in **Settings → Agent**.
- **"Iteration limit reached".** The agent hit its per-message cap on a long task — not a bug, a budget. Raise it in **Settings → Agent → Turn budget** (default 75, up to 500), or just say "continue".
- **Widget previews look wrong / the agent says it can't see.** Text-only chat model: set `CHAT_VISION=false` and optionally `VISION_MODEL` to a small vision model for captions.
- **You changed `EMBED_MODEL`.** The vector dimension is fixed per table — reset the RAG data (`docker compose down -v` wipes everything, or drop the `rag_*` tables) and re-index.
- **Upgrading.** `docker compose pull && docker compose up -d` (or `--build` if you build locally). Schema migrations run at start, nothing to do — [docs/UPGRADING.md](docs/UPGRADING.md) is the contract.
- **Timezone.** `TZ` accepts IANA names; for a fixed offset use `Etc/GMT+4` — POSIX inverts the sign, so that's UTC-4.

More on daily use in [docs/help/](docs/help/) — it's also the built-in help inside the app.

---

## License

MIT
