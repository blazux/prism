# <img src="logo.svg" alt="Prism logo" height="22"> PRISM

**Programmable Responsive Interface for Smart Models**

*Also known as: Probably Runs Interesting Stuff Magically*

A self-hosted AI workspace that runs entirely on your own hardware. No cloud, no API keys, no data leaving your machine.

<img src="gui.png" width="700" alt="PRISM dashboard">

<details>
<summary>More screenshots</summary>

<img src="chat.png" width="700" alt="Chat with the agent">
<img src="settings.png" width="700" alt="Agent settings">

</details>

---

## What you can do with it

PRISM gives you a local AI assistant that can actually *do* things, not just chat. It runs in a sandboxed environment and has access to a growing set of capabilities:

**Code & automation** — Ask the agent to write and run code, install packages, execute shell commands, and schedule recurring tasks with cron. It can build fully functional scripts and services directly in the workspace.

**Dashboard widgets** — The agent can create interactive widgets (HTML/JS) and pin them to a drag-and-drop dashboard. Live data feeds, clocks, monitoring panels, custom tools — anything that renders in a browser.

**Web & research** — The agent can search the web via a self-hosted SearXNG instance, fetch and parse URLs, and browse pages. Combine this with the knowledge base to build a private research assistant.

**Knowledge base (RAG)** — Upload documents (PDF, Markdown, plain text) and the agent will index them locally with embeddings. It searches the knowledge base automatically when relevant, keeping your data entirely private.

**Browser automation** — The agent can control a browser to interact with web pages, fill forms, scrape content, and automate repetitive tasks.

**Notifications & scheduling** — The agent can send notifications to the dashboard and schedule them for later, making it useful for reminders and monitoring.

**Extensible tools** — The agent can define and register its own new tools at runtime, extending its own capabilities without touching the code.

**Secrets** — Sensitive values (API keys, passwords) can be stored securely and accessed by the agent on demand without exposing them in the conversation.

**Persistent memory** — Conversation history is stored per session and automatically summarized when it gets long. Each session can have its own personality and context.

---

## Stack

| Layer | Tech |
|---|---|
| Backend | Go |
| Frontend | Vanilla JS, GridStack |
| LLM | Ollama (any model) |
| Embeddings | Ollama (any embedding model) |
| Vector store | PostgreSQL + pgvector |
| Web search | SearXNG |
| Runtime | Docker / Docker Compose |

---

## Requirements

- Docker + Docker Compose
- An [Ollama](https://ollama.com) instance (local or remote) with at least one model and one embedding model pulled

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

## Configuration

All configuration is done via environment variables (set in `docker-compose.yml` or a `.env` file):

| Variable | Description | Example |
|---|---|---|
| `OLLAMA_URL` | URL of your Ollama instance | `http://ollama:11434` |
| `OLLAMA_MODEL` | LLM model to use | `qwen3.6:27b` |
| `EMBED_MODEL` | Embedding model for RAG | `qwen3-embedding:8b` |
| `POSTGRES_URL` | PostgreSQL connection string | `postgres://rag:rag@postgres:5432/rag` |
| `SEARXNG_URL` | SearXNG instance URL (optional) | `http://searxng:8080` |
| `WORKSPACE_DIR` | Agent workspace directory | `/workspace` |
| `PLUGIN_DIR` | Widget storage directory | `/workspace/.plugins` |

> **The only required changes are `OLLAMA_URL` and the model names** — everything else (Postgres, SearXNG, paths) works out of the box with the default Docker Compose setup and can be left untouched unless you have specific needs.

> **Note on embedding models:** the vector dimension is detected automatically at startup by probing the model. If you change the embedding model, you need to reset the RAG database (the vector dimension is fixed per table).

---

## License

MIT
