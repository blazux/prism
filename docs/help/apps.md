# The apps

All of these can be driven from chat — the agent has a tool for each. Open them
from the command palette (Ctrl+K) or by asking.

## Email
The Email app reads and sends messages through the IMAP/SMTP mailbox configured in Settings → Email.
You can triage/categorize with tags, filter by tag, search, read, reply, and send
**with attachments**. The agent can summarize your inbox, turn an email into a
task or a calendar event, and (with cron) send you a morning digest over Telegram.
Browse and manage folders, move/archive/trash messages, mark read/unread and
create deterministic inbox rules. See **email** for the full UI and agent workflow.

## Calendar
The Calendar app manages events with a title, time, location and description.
It stores them in Prism’s database by default, or uses a connected account: **CalDAV** (Apple iCloud, Nextcloud,
Fastmail), **Google Calendar** or **Microsoft / Outlook** (both via your own
OAuth app — see their dedicated guides). Connect them in Settings → Calendar;
when several are connected, **Active sources** there picks which one the app
uses. Ask the agent to "add lunch with Sam Friday at noon" or "what's on next
week". It can also edit existing events with only changed fields. All-day and
multi-day events have matching interface and tool controls.

## Tasks
The Tasks app tracks to-do items with priorities and due dates. It stores them
in Prism’s database or uses **CalDAV**
(Apple Reminders / Nextcloud Tasks), or **Todoist** when connected. The agent can
add, edit, complete, reopen and delete tasks, filter by deadline or priority,
and break an objective into tasks. See **calendar-tasks** for both workflows.

The same app lists the agent's **scheduled jobs** (cron) under your to-dos, each
with a pause toggle, an edit form and a delete button. The agent does the same
from chat: "pause the morning digest", "resume it", "run the backup at 3am
instead", "delete the feed sync".

## Notes
The Notes app provides Markdown notes with `[[wikilinks]]` and a split editor
with an AI toolbar. Its **Add to knowledge** button sends a note to a document
collection for retrieval. Notes live in
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

## Link to an app item

Use an ordinary Markdown link in a reply or an HTML link in a widget to open an
item in its app. Use the exact ID returned by the corresponding tool, and URL-encode
each value. Use **Copy link** in an open note, task, event or message to copy its URL.
For email, the ID is the message UID and `folder` is its exact mailbox folder.

- Note: `[Open the note](/?app=notes&id=42)`
- Task: `[Open the task](/?app=tasks&id=17)`
- Event: `[Open the event](/?app=calendar&id=23)`
- Email: `[Read the message](/?app=email&id=153&folder=INBOX)`
- Conversation: `[Open this workspace](/?workspace=finance&chat=open)`

These links use the signed-in user's connections and permissions. They do not
select another user's account. A recipient may not have access to the same item.
IDs belong to the connected source: switching providers can change their meaning.
An email UID identifies a message only in the same mailbox and folder; an email
link is not a portable identifier for sharing that message with another account.
Missing items and disconnected sources report an error. Files can already be
opened from widgets with `prismOpenFile(path)`; use attachment links for downloads.

## Terminal
The Terminal opens an interactive shell in the agent’s workspace container.
Toggle it with **Ctrl+Enter**. It supports terminal programs such as `vim` and
`htop`, colors and package installation. Use it when you need direct access to
the environment where the agent runs.

## Activity
The Actions view shows completed changes, failures and inbox rule results;
technical details remain available in separate tabs. See **activity**.
