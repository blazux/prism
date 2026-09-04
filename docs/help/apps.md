# The apps

All of these can be driven from chat — the agent has a tool for each. Open them
from the command palette (Ctrl+K) or by asking.

## Email
Reads and sends through your IMAP/SMTP mailbox (configured in Settings → Email).
You can triage/categorize with tags, filter by tag, search, read, reply, and send
**with attachments**. The agent can summarize your inbox, turn an email into a
task or a calendar event, and (with cron) send you a morning digest over Telegram.
Prism is a communication *aid*, not a full mail client — there are no folders or
filters by design.

## Calendar
Events with title, time, location and description. Backed by Prism's database by
default, or by a connected account: **CalDAV** (Apple iCloud, Nextcloud,
Fastmail), **Google Calendar** or **Microsoft / Outlook** (both via your own
OAuth app — see their dedicated guides). Connect them in Settings → Calendar;
when several are connected, **Active sources** there picks which one the app
uses. Ask the agent to "add lunch with Sam Friday at noon" or "what's on next
week".

## Tasks
To-do items with priority and due date. Backed by Prism's database, **CalDAV**
(Apple Reminders / Nextcloud Tasks), or **Todoist** when connected. The agent can
add tasks, mark them done, and break a big task into subtasks.

## Notes
Markdown notes with `[[wikilinks]]`, a split editor with an AI toolbar, and an
"Add to knowledge" button that pushes a note into a RAG collection. Notes live in
Prism's database by default, or in an **Obsidian / Logseq vault** (a folder of
`.md` files) when connected in Settings → Notes. The agent can create and edit
notes for you.

## Terminal
A real interactive terminal into the agent's workspace container. Toggle it with
**Ctrl+Enter**. Full TTY — `vim`, `htop`, colours, package
installs all work. Useful for power users who want direct control of the
environment the agent runs in.
