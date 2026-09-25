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
- schedule, edit, pause and remove **cron jobs**;
- build, update and share **widgets and dashboards**;
- save **skills** (procedures it learned) and register **custom tools**
  (Python scripts);
- store **secrets** — it asks for the value through a secure dialog, the value
  never goes through the chat — and share one with your group;
- set up your **email account** (IMAP/SMTP host, user, password, STARTTLS and
  self-signed options — ProtonMail Bridge included);
- connect **CalDAV** and **Todoist**, point notes at a **Markdown vault**, and
  choose the **active source** for calendar and tasks (credentials go through
  the secure dialog, never through the chat);
- connect or unlink your **Telegram bot**;
- create, edit, pause and remove **webhooks**;
- change its own **settings**: name, personality, turn budget, extended
  reasoning, lean prompt, reasoning effort;
- add, remove, disable or enable **MCP servers** (personal mode only);
- share a **note** with your group;
- roll a workspace file back to an earlier version.

What stays in Settings or the Admin console: anything that needs a browser
sign-in (Google and Microsoft OAuth), the deployment-wide channels (Slack,
Webex), and — in a shared deployment — everything group-scoped (group knowledge
base, MCP servers, tool policy, the shared agent). The agent still knows these
pages and can walk you through them step by step.

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

## Background work and subagents

A dashboard task keeps running when you open Settings, switch boards or close
the browser. Return to the same conversation to see its progress and results.
The board list marks running tasks and tasks waiting for input. **Stop** cancels
that conversation's task and its subagents; closing the page does not.

Manual tool approvals and secure credential requests wait for you and reappear
when you reconnect. The approval mode is fixed for the current task. Unsaved
forms still belong to the browser tab that opened them: the agent cannot edit a
closed tab's draft. Save important drafts before leaving.

For substantial independent work, the agent can use `subagent` to start helpers,
wait for their results or stop them. Helpers use the same model, workspace and
permissions, with separate conversations. They receive the task and context the
parent supplies. They cannot delegate again, request credentials or control your
open editor. Assign distinct files when work happens in parallel. The parent
must collect results before finishing: unfinished helpers stop with it.

There is one active task per conversation, up to four per user and sixteen per
server. A task lasts at most two hours, including approval waits. Up to three
helpers can run at once, six per task, for at most fifteen minutes each. Parent
and helpers share the main agent-loop model-call budget; this is not a monetary
spending limit for provider APIs or code the agent runs.

A server restart interrupts active tasks. Prism shows the interruption and keeps
saved conversation history; it does not automatically replay operations that may
already have changed files or sent messages. Review the results before asking to
continue. Without a database, recent results are only retained in memory. Phone
calls keep their existing behavior: hanging up stops the voice agent.
