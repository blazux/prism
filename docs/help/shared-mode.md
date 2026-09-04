# Shared mode (multi-user deployments)

Prism runs in one of two modes, chosen at deployment (`MULTI_USER=1`). In
**personal** mode there is one user and everything is in Settings. In **shared**
mode a team uses one Prism: everyone gets their own agent, groups get a shared
one, and an **Admin console** appears next to Settings. This page is about the
shared mode.

## Accounts
- Sign up with email and password. The **first** account becomes the global
  admin and is approved automatically.
- Every later signup lands as *pending* — the signup page says *"Account
  created — waiting for an admin to approve it."* Global admins get a
  notification and approve it in **Admin console → Users** (**Approve**,
  **Disable**, **Make admin** / **Revoke admin**).

## Groups and roles
The global admin creates groups in **Admin console → Groups** and adds members
with a role: **member** or **admin**. Per-group model grants live there too.

- **Global admin** — sees the whole console: Users, Groups, Tools (global tool
  policy), Platform (hide apps, allowed models), Usage, Logs, plus Telephony
  when a phone stack is docked.
- **Group admin** — opens the same console (the **Administration** link in the
  header) but only sees the panes for their groups: **Shared agent**, **RAG**,
  **MCP**, **Secrets**, **Tool access**.
- **Member** — no console. Settings only.

## The Room and the shared agent
Members of a group get a **Room** app in the left rail: a chat where the group
talks together and to the group's **shared agent**. Pick the group at the top;
the agent answers only when **@mentioned** — `@agent`, or `@Name` with the
name set by the group admin (spaces removed). While it works, *"Name is
thinking…"* appears. On each mention it also receives the messages posted since
its last reply, so it has the context of the conversation it wasn't part of.

The shared agent runs with the rights the group admin gives it (the global tool
policy tightened by the group's restrictions, at member level), uses the group's
knowledge base, MCP servers and secrets, and is the same agent that answers on
the group's Webex bot. Its name, avatar, model, system prompt and turn budget
are set in **Admin console → Shared agent**.

## What is group-scoped
Configured by a group admin in the **Admin console**; members see it read-only
in their Settings.

| Resource | Where it's managed | What members see |
|---|---|---|
| Knowledge base (RAG) | Admin → RAG | Settings → Knowledge, read-only |
| MCP servers | Admin → MCP | Settings → MCP, read-only; their tools in Settings → Tools |
| Group secrets | Settings → Secrets (any member) or Admin → Secrets | Settings → Secrets |
| Tool access | Admin → Tool access | badges in Settings → Tools |
| Shared widgets gallery | **Shared widgets** button in the header, share button on each widget | same gallery |
| Webex bot | Admin → Shared agent | — |

There are **no personal** MCP servers or knowledge collections in shared mode:
a member with no group has neither, and Settings says so (*"You're not part of
a group yet — ask an admin to add you to one"*).

## What stays personal
Your own agent (name, personality, avatar, turn budget), your workspaces and
chat history, your memory and skills, your Notes, Tasks, Calendar and Email
integrations (each member connects their own mailbox, vault, CalDAV, Google or
Microsoft account), your personal secrets and your Telegram bot. Group things
are additive: your agent *gains* the group's knowledge base and MCP tools, it
doesn't lose yours.

## Admin-only
- **Slack** is a single deployment-wide bot: its card in **Settings → Channels**
  appears only for global admins, and only they can set or clear its tokens.
- The **Terminal** (Ctrl+Enter) and one-shot command execution are reserved to
  global admins and group admins — they open a shell on the shared workspace.
- **Admin → Platform** can hide an app (Email, Notes, Calendar, Room…) for
  everyone and restrict which chat models users may pick; **Admin → Tools**
  sets the global tool policy (see the tools help).
- **Admin → Usage** shows activity counts, an audit trail of refused tool
  calls and recent errors — no conversation content.

## Note on switching modes
Turning `MULTI_USER` on is a one-way door: at the first start in shared mode,
Prism moves the existing configuration and secrets into the scope of the first
admin — so the person who set Prism up must be the first to sign up.
