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
| LLM | Ollama — or any OpenAI-compatible server (vLLM, SGLang, TGI, …) |
| Embeddings | Ollama (any embedding model) |
| Vector store | PostgreSQL + pgvector |
| Web search | SearXNG |
| Service routing | Traefik |
| Messaging | Telegram / Slack bridge |
| Integrations | Email (IMAP/SMTP), CalDAV, Todoist, Google, Microsoft, Obsidian/Logseq |
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
- **Reach it from anywhere** — a **Telegram** (and **Slack**) bridge, so you can bother the agent from your phone while pretending to work.

Connecting an account is a one-time OAuth click or an app password in **Settings**. Prism never hosts a shared OAuth app — you create your own, because your calendar is nobody's business but yours.

---

## Bring your own model

Prism talks to **Ollama** by default, but it'll happily point at any **OpenAI-compatible** server — vLLM, SGLang, TGI, LM Studio, llama.cpp, even OpenRouter if you insist on sending your data to the cloud (we won't tell). Wire *both* at once and the model picker spans them: a fast local model for daily driving, a 120B reasoner for when you're feeling ambitious — switch per message from the dropdown. Embeddings can stay on Ollama while chat runs elsewhere, because not every inference server bothers to implement `/v1/embeddings`.

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
| `LLM_BACKEND` | `ollama` (default) or `openai` for any OpenAI-compatible server | `openai` |
| `OPENAI_BASE_URL` | The `/v1` root, when `LLM_BACKEND=openai` | `http://host:8000/v1` |
| `OPENAI_MODEL` | Chat model name for the openai backend (its `--served-model-name`) | `qwen` |
| `EMBED_BACKEND` | Set to `ollama` to keep RAG on Ollama while chat runs on an openai server with no `/v1/embeddings` | `ollama` |
| `PRISM_TOKEN` | Optional login token — protects the dashboard (omit to disable auth) | `change-me` |
| `POSTGRES_URL` | PostgreSQL connection string | `postgres://rag:rag@postgres:5432/rag` |
| `SEARXNG_URL` | SearXNG instance URL (optional) | `http://searxng:8080` |
| `WORKSPACE_DIR` | Agent workspace directory | `/workspace` |
| `PLUGIN_DIR` | Widget storage directory | `/workspace/.plugins` |
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
