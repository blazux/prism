# What Prism is

Prism is a self-hosted AI dashboard. The heart of it is an **agent** — your
assistant — and the apps (Email, Calendar, Tasks, Notes, Terminal) are the
surfaces it acts on. You ask in chat; the agent does the work, connects the apps
together, and can even build custom dashboards and widgets for you. Everything
runs on your own machine, against your own LLM.

## The big picture

- **Chat is the control center.** Talk to the agent to read mail, schedule
  events, manage tasks, take notes, research the web, run commands, or build a
  widget. Buttons exist for convenience, but the agent is the main way to get
  things done.
- **Workspaces** are separate boards (left rail). Each has its own chat history,
  personality adaptation, widgets and dashboards. Personal data (calendar,
  tasks, notes) is shared across all workspaces.
- **Widgets & dashboards** are HTML the agent builds and pins to a board — live
  views over your data or external APIs. Ask the agent to "make a dashboard for
  X" rather than configuring one by hand.

## Connecting your real accounts

Prism can keep data in its own database, or bridge to your real services:

- **Email** — your IMAP/SMTP mailbox (Settings → Email).
- **Notes** — an Obsidian / Logseq Markdown vault (Settings → Notes).
- **Calendar & Tasks** — a CalDAV account: Apple iCloud, Nextcloud, Fastmail…
  (Settings → Calendar).
- **Tasks** — Todoist via a personal token (Settings → Calendar).

See the "connecting-accounts" and provider-specific help for step-by-step setup.

## Asking the agent for help

The agent has access to this documentation. Ask it things like "how do I connect
my iCloud calendar?", "what can you do?", or "help me set up Google Calendar" and
it will walk you through it.
