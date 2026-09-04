# Working with the agent

The agent is the point of Prism. Treat it like a capable assistant with hands on
all your tools, not a search box.

## What it can do
- **Operate the apps**: read/triage/send email, add or query calendar events,
  manage tasks, create and edit notes.
- **Connect things**: "turn this email into a task", "block an hour tomorrow for
  the report", "save this research as a note".
- **Research**: web search and deeper multi-step research; read web pages.
- **Knowledge (RAG)**: ingest documents into collections and answer from them;
  the "Add to knowledge" button in Notes feeds the same store.
- **Build UI**: create widgets and dashboards (HTML) pinned to a board — ask for
  "a dashboard showing my unread mail and today's events".
- **Run code**: execute commands, install packages, manage Docker, all inside its
  workspace container (you also get a terminal with Ctrl+Enter).
- **Automate**: schedule recurring jobs (cron), e.g. a morning email summary
  delivered to Telegram.
- **Remember**: it keeps a profile of you and can search past conversations.

## Tips
- Refer to what you're looking at: "summarize *this* email", "move *this* event".
  The agent gets the on-screen context.
- Ask it to *do*, not just tell: "schedule it", "send the reply", "make the
  widget".
- It can change its own default personality and per-workspace behavior — see
  Settings → Agent, or just ask.
- Want to check each step before it runs? Switch the **Approval** dropdown in
  the chat header to **manual** — see the tools help.

## Ask the agent to configure things
Much of Prism can be set up from chat rather than from Settings. The agent can
itself:
- create **knowledge collections** and index workspace files into them;
- schedule and remove **cron jobs**;
- build, update and share **widgets and dashboards**;
- save **skills** (procedures it learned) and register **custom tools**
  (Python scripts);
- store **secrets** — it asks for the value through a secure dialog, the value
  never goes through the chat;
- set up your **email account** (IMAP/SMTP host, user, password);
- add or remove **MCP servers** (personal mode only);
- roll a workspace file back to an earlier version.

What stays in Settings or the Admin console: channel tokens (Telegram, Slack,
Webex), OAuth accounts (Google, Microsoft, CalDAV, Todoist), the notes vault,
and — in a shared deployment — everything group-scoped (group knowledge base,
MCP servers, tool policy, the shared agent).

## Channels
- **Web**: the main dashboard chat.
- **Telegram**: link your own bot in Settings → Channels to chat with your
  agent from your phone; cron jobs can push messages there too.
- **Slack**: a single Socket Mode app for the whole deployment, connected by a
  **global admin** in Settings → Channels (the Slack card is only shown to
  them); cron jobs can deliver there too (deliver="slack").
- **Webex** (shared deployments): each group's bot talks to the group's shared
  agent — set up by a group admin in the Admin console.

## Your profile

**Settings → Profile** holds how you appear to others: a photo (Upload photo / remove), a display name (how you appear in chat and in group rooms), your first and last name and a phone number. Your login email is shown there for reference.
