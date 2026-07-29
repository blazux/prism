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
| Voice | Optional phone line via [PrismConnect](https://github.com/blazux/PrismConnect) — the agent answers and places calls |
| Modes | Personal (single user) or shared (accounts, groups, rooms) — one flag |
| Runtime | Docker / Docker Compose |

---

## Requirements

- Docker + Docker Compose
- An [Ollama](https://ollama.com) instance (local or remote) with at least one model and one embedding model pulled — or any OpenAI-compatible server for chat, with Ollama still handling embeddings

---

## Quick start

```bash
git clone https://github.com/blazux/prism
cd prism
cp .env.example .env   # edit to point at your Ollama instance
docker compose up -d
```

Open [http://localhost:48080](http://localhost:48080).

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

## Personal, or shared

Prism is a personal dashboard by default: one user, and `PRISM_TOKEN` is the whole of authentication. But the same binary runs a shared deployment — set `MULTI_USER=1` and it grows accounts, a login and signup page, groups, per-user scoping for every integration, an admin console, and **Rooms** (shared chat spaces where several people talk to the same agent). The first person to sign up becomes the global admin.

It's the same codebase either way; the mode is decided in one place. At home you never see any of it.

> **Heads up:** turning `MULTI_USER` on is a one-way door. It migrates this deployment's config keys and secrets into the first admin's scope, and single-user mode would no longer find them. Decide before you flip it, not after.

---

## Give it a phone

Point `VOX_URL` at a [PrismConnect](https://github.com/blazux/PrismConnect) instance and the agent gains a phone line. It can **answer** incoming calls with its full toolset — RAG, memory, personality, the lot — and **place**, **list** and **cancel** outgoing calls itself, as ordinary agent tools (`place_call`, `list_calls`, `cancel_call`). The call queue shows up in Tasks. Leave `VOX_URL` empty and none of this exists.

---

## Bring your own model

Prism talks to **Ollama** by default, but it'll happily point at any **OpenAI-compatible** server — vLLM, SGLang, TGI, LM Studio, llama.cpp, even OpenRouter if you insist on sending your data to the cloud (we won't tell). Wire *both* at once and the model picker spans them: a fast local model for daily driving, a 120B reasoner for when you're feeling ambitious — switch per message from the dropdown. Embeddings can stay on Ollama while chat runs elsewhere, because not every inference server bothers to implement `/v1/embeddings`.

There's also an **Anthropic** backend (`LLM_BACKEND=anthropic`) for driving Prism with Claude: set `ANTHROPIC_API_KEY`, pick a model, and Claude sits in the same picker as your local ones. RAG stays on Ollama automatically, since Anthropic serves no embeddings.

> **A Claude Pro/Max subscription will not work, and this is deliberate.** The OAuth token the `claude` CLI stores does authenticate, and plain chat runs on it — but Anthropic classifies a tool-bearing request from anything that isn't Claude Code as a third-party app and bills it against *extra usage* rather than plan limits, so the agent loop is refused. The refusal is intermittent and no model or setting avoids it ([hermes-agent#31668](https://github.com/NousResearch/hermes-agent/issues/31668) is the same wall from the other side, closed with no fix). Prism is an agent, so a brain that drops tool calls at random is worse than no brain: it was implemented, measured, and taken out rather than shipped as a trap. Paste that token into `ANTHROPIC_API_KEY` and Prism tells you why it won't work instead of letting Anthropic answer `401`.

---

## It remembers, and occasionally learns

Prism keeps a memory of who you are and how you work, records lessons from problems it stumbled through, and saves multi-step jobs as reusable **skills** — so the second time you ask, it doesn't reinvent the wheel. Long conversations get summarized automatically, and it can full-text search everything you've ever discussed, which means it (mostly) stops asking you to repeat yourself. Secrets you hand it are stored **AES-256-GCM encrypted**, not in a plaintext file it'll cheerfully commit to Git.

---

## Configuration

All configuration is done via environment variables (set in `docker-compose.yml` or a `.env` file):

| Variable | Description | Example |
|---|---|---|
| `OLLAMA_URL` | URL of your Ollama instance | `http://ollama:11434` |
| `OLLAMA_MODEL` | LLM model to use | `qwen3.6:27b` |
| `EMBED_MODEL` | Embedding model for RAG | `qwen3-embedding:8b` |
| `LLM_BACKEND` | `ollama` (default), `openai` for any OpenAI-compatible server, or `anthropic` for Claude | `openai` |
| `OPENAI_BASE_URL` | The `/v1` root, when `LLM_BACKEND=openai` | `http://host:8000/v1` |
| `OPENAI_MODEL` | Chat model name for the openai backend (its `--served-model-name`) | `qwen` |
| `OPENAI_API_KEY` | Key for the openai backend, if it needs one (local servers usually don't) | `sk-…` |
| `ANTHROPIC_MODEL` | Chat model, when `LLM_BACKEND=anthropic` | `claude-sonnet-5` |
| `ANTHROPIC_API_KEY` | Anthropic API key from console.anthropic.com. A subscription token is not accepted — see above | `sk-ant-api03-…` |
| `PRISM_TOKEN` | Login token — protects the dashboard (omit to disable auth) | `change-me` |
| `MULTI_USER` | `1` to turn on accounts, groups, rooms and the admin console (default: personal, single-user) | `1` |
| `VOX_URL` | A [PrismConnect](https://github.com/blazux/PrismConnect) instance — gives the agent a phone (empty = no telephony) | `http://prismconnect:7860` |
| `VOX_USER` / `VOX_PASSWORD` | Credentials for the PrismConnect instance, if it's protected | `agent` / `…` |
| `POSTGRES_URL` | PostgreSQL connection string | `postgres://rag:rag@postgres:5432/rag` |
| `SEARXNG_URL` | SearXNG instance URL (optional) | `http://searxng:8080` |
| `AGENT_CONTAINER` | Name of the workspace container the agent runs code in | `prism-workspace` |
| `WORKSPACE_DIR` | Agent workspace directory | `/workspace` |
| `PLUGIN_DIR` | Widget storage directory | `/workspace/.plugins` |
| `CHAT_VISION` | `false` if the chat model is text-only (e.g. MiniMax) — widget previews are captioned to text instead of shown | `false` |
| `VISION_MODEL` | Override the model used to caption widget previews (defaults to the chat model) | `qwen3-vl` |
| `TZ` | Timezone for the agent and cron jobs | `America/New_York`, `Europe/Paris` |
| `SERVICE_PORT_START` | First host port in the auto-allocation range | `20000` |
| `SERVICE_PORT_END` | Last host port in the auto-allocation range | `20999` |

> **The only required changes are `OLLAMA_URL` and the model names** — everything else works out of the box with the default Docker Compose setup.

> **Two models, one menu:** set `LLM_BACKEND` to whichever is your default, but fill in *both* `OLLAMA_URL` and `OPENAI_BASE_URL` and the model picker will list models from both servers, routing each pick to the one that serves it. Handy for a fast local default with a heavyweight reasoner kept one click away.

> **Note on embedding models:** the vector dimension is detected automatically at startup by probing the model. If you change the embedding model, you need to reset the RAG database (the vector dimension is fixed per table).

> **Note on timezone:** `TZ` defaults to `UTC`. Set it to your local timezone (IANA format, e.g. `America/New_York`, `Europe/Paris`, `Asia/Tokyo`) so that cron schedules and agent timestamps match your local time. For a fixed offset without daylight saving, use `Etc/GMT+4` (note: POSIX convention inverts the sign — `Etc/GMT+4` = UTC-4).

---

## License

MIT
