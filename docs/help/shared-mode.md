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
- A **member** uses personal Settings and has no access to the administration
  console unless they also hold an administrator role.

## Shared widgets gallery

Open **Shared widgets** in the header to browse your group's publications.
Each card shows its title, author, optional description and a static preview.
Click the preview to enlarge it, then **Add to workspace** to copy the widget
to the current workspace. You can also select several cards and **Add selected**.

Use a widget's **Share** button or the gallery's **Share** tab to publish it.
An optional description (up to 1,000 characters) explains what it does. Prism
automatically captures the visible widget; a dashboard uses its first widget
as the cover. The image may contain the data visible at publication time.
The capture stays with the share even if the original widget is removed.
Browsing previews never executes the shared widget's code.

The agent can do the same with `widget action=share id=<widget-id>
description="..."`. It needs no vision model to generate a preview.

Existing shares remain available after upgrading, with no description or
preview until republished. **No preview available** also appears if the
capture browser was unavailable; this does not prevent sharing or adding.
Previews follow the share's group access. Unsharing deletes the publication
and its preview, while copies already added to workspaces are kept.

## The Room and the shared agent
Members of a group get a **Room** app in the left rail: a chat where the group
talks together and to the group's **shared agent**. Pick the group at the top;
the agent answers only when **@mentioned** — `@agent`, or `@Name` with the
name set by the group admin (spaces removed). Responses stream into the Room. Expand a tool call to inspect its arguments,
result or error; these traces remain visible after reloading. Requests to the
shared agent are processed in order, and **Stop** interrupts the current response.

**Earlier messages** pages through the room archive. The active model context is
a short recent window with a retained summary. Recent room chatter is also
bounded before being handed to the agent. **Agent context → Lighten context**
summarizes older exchanges without deleting messages or tool results. The agent
can search its earlier exchanges and read older room messages when needed.

The shared agent runs with the rights the group admin gives it (the global tool
policy tightened by the group's restrictions, at member level), uses the group's
knowledge base, MCP servers and secrets, and is the same agent that answers on
the group's Webex bot. Its name, avatar, model, system prompt and turn budget
are set in **Admin console → Shared agent**.

## What is group-scoped
Group administrators manage the knowledge base, MCP servers and tool access in
the **Admin console**. Members see the knowledge and MCP settings in read-only
form. Group secrets can also be managed by members, as shown below.

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
Your agent’s name, personality, avatar and turn budget remain personal, as do
your workspaces, chat history, memory and skills. Each member connects their own
mailbox, notes vault, calendar and task accounts. Personal secrets and Telegram
bots also remain individual. Group membership adds access to the group’s
knowledge base and MCP tools without replacing these personal settings.

### Existing skill files when upgrading

Personal skills are stored in `skills/u<user-id>/` under the execution
workspace; shared-agent skills use `skills/g<group-id>/`. The agent selects its
library automatically. Personal mode continues to use `skills/` directly.

Older shared deployments may have skill JSON files directly in `skills/`.
Those files are preserved but are not loaded into users' or groups' libraries.
An administrator can review them and copy each file into the appropriate owner
directory. Keep a copy of the original files until the intended agent can list
and read the procedures. The execution workspace and administrator shell access
remain shared infrastructure for a trusted team.

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

## The PrismVox workspace

When PrismVox is connected, a global administrator can authorize existing groups
from **PrismVox → Access**. Members then see **PrismVox** in the workspace list.
Use it to ask about the standard's work and consult call history. Its supervision
conversation belongs to the selected group. Personal recognized calls are not
shared with that group; older calls with no recorded audience stay in the private
**Administrator archive**. SIP, speech and model settings remain in **Telephony
settings**. PrismVox also works in personal Prism deployments; see [Telephony](telephony.md).
