# The apps

All of these can be driven from chat — the agent has a tool for each. Open them
from the command palette (Ctrl+K) or by asking.

## Email
Reads and sends through your IMAP/SMTP mailbox (configured in Settings → Email).
You can triage/categorize with tags, filter by tag, search, read, reply, and send
**with attachments**. The agent can summarize your inbox, turn an email into a
task or a calendar event, and (with cron) send you a morning digest over Telegram.
Browse and manage folders, move/archive/trash messages, mark read/unread and
create deterministic inbox rules. See **email** for the full UI and agent workflow.

## Calendar
Events with title, time, location and description. Backed by Prism's database by
default, or by a connected account: **CalDAV** (Apple iCloud, Nextcloud,
Fastmail), **Google Calendar** or **Microsoft / Outlook** (both via your own
OAuth app — see their dedicated guides). Connect them in Settings → Calendar;
when several are connected, **Active sources** there picks which one the app
uses. Ask the agent to "add lunch with Sam Friday at noon" or "what's on next
week". It can also edit existing events with only changed fields. All-day and
multi-day events have matching interface and tool controls.

## Tasks
To-do items with priority and due date. Backed by Prism's database, **CalDAV**
(Apple Reminders / Nextcloud Tasks), or **Todoist** when connected. The agent can
add, edit, complete, reopen and delete tasks, filter by deadline or priority,
and break an objective into tasks. See **calendar-tasks** for both workflows.

The same app lists the agent's **scheduled jobs** (cron) under your to-dos, each
with a pause toggle, an edit form and a delete button. The agent does the same
from chat: "pause the morning digest", "resume it", "run the backup at 3am
instead", "delete the feed sync".

## Notes
Markdown notes with `[[wikilinks]]`, a split editor with an AI toolbar, and an
"Add to knowledge" button that pushes a note into a RAG collection. Notes live in
Prism's database by default, or in an **Obsidian / Logseq vault** (a folder of
`.md` files) when connected in Settings → Notes. The agent can create and edit
notes for you. With a note open, ask for a change directly in chat: the agent
uses `editor action=read`, then `editor action=update` with the returned revision
and only the fields to change (`title`, `body`, `tags`). It sees unsaved input;
its changes appear in the editor and are saved through the same note provider.
For stored notes that are not open, the normal `note` tool still applies.
The open Notes app also refreshes after tools complete, including file tools
used on a connected vault. This updates after a completed change, not token by
token during generation. If a remote change conflicts with local typing, both
versions are retained until the user selects **Load updated note** or **Keep my
edits**. Failed saves leave the text visible and report failure.

In a shared deployment a note can be **shared with your group**
(read-only for members) from the note's share button or by asking the agent
("share my onboarding note with the team"); sharing it again refreshes the copy,
and the author or a group admin can unshare it.

## Terminal
A real interactive terminal into the agent's workspace container. Toggle it with
**Ctrl+Enter**. Full TTY — `vim`, `htop`, colours, package
installs all work. Useful for power users who want direct control of the
environment the agent runs in.

## Activity
The Actions view shows completed changes, failures and inbox rule results;
technical details remain available in separate tabs. See **activity**.
