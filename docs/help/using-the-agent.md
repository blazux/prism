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

## Channels
- **Web**: the main dashboard chat.
- **Slack**: connect a Socket Mode app in Settings → Channels to chat with the
  agent from Slack; cron jobs can deliver there too (deliver="slack").
- **Telegram**: link a bot in Settings → Channels to chat with the agent from
  your phone; cron jobs can push messages there too.
