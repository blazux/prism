# Tools, opt-outs and manual approval

The agent works through *tools* — run a command, read mail, search the web,
add a widget… Three layers decide which tools it actually gets, and whether it
may use them without asking you first.

## Settings → Tools: your own opt-outs
**Settings → Tools** lists the built-in tools by category (Dashboard, Files,
System, Web, Personal, Scheduling, Knowledge (RAG)), followed by your **Custom
Tools** (scripts the agent registered), **MCP Tools** (single-user mode) and
**Group MCP Tools** (shared deployments). Each card has a toggle:

- Toggle a tool **off** and it is simply left out of the agent's toolset — it
  no longer sees it at all. Toggle it back on at any time.
- Built-in and custom-tool opt-outs are stored **in this browser** and sent
  with each message, so another browser or device starts from the defaults.
- Group MCP opt-outs are stored on your account. A group MCP tool locked by an
  admin shows a badge instead of a toggle: **🔒 admins only** or
  **🚫 disabled by admin**.

## Manual tool approval
By default the agent runs its tool calls immediately. To review each one
first, use the **Approval** dropdown in the chat header:

- **⚡ Approval: auto** — run tool calls as they come (default).
- **✋ Approval: manual** — pause before *every* tool call.

In manual mode, each tool block in the chat shows the **full** command or
arguments (the complete shell command for `exec_command`, the target path and
content for `write_file`, the full JSON otherwise) with *Waiting for your
approval…* and two buttons: **✓ Approve** and **✕ Reject**.

- **Approve** runs the call; the block switches to *Running…*.
- **Reject** skips it. The agent is told *"Tool call rejected by the user. Do
  not retry it as-is — ask what they want done differently."*, so it comes
  back with a question rather than trying the same thing again.

The choice is remembered per browser. It applies to the dashboard chat only:
Telegram, Slack, Webex, webhooks and cron jobs have nobody to click, so they
run in auto mode, and phone calls always bypass it.

## Tool policy set by admins (shared deployments)
In a multi-user deployment, admins set a ceiling on what members' agents may
call:

- **Admin console → Tools** (global admin): each tool is **Open to members**,
  **Admins only** (members' agents are blocked) or **Disabled** (nobody — admins
  and shared agents included). Everything is open by default, except the
  outbound-call tools (`place_call`, `list_calls`, `cancel_call`), which start
  as admins-only.
- **Admin console → Tool access** (group admin): toggle a tool **OFF** to
  restrict it to admins for that group. A group can only *tighten* the global
  policy, never loosen it — tools locked globally show as *admin-only* and
  cannot be toggled.
- Global admins and group admins are not gated by the policy (except by
  **Disabled**, which hides the tool for everyone). The group's shared agent
  always runs at member level.

## Why a tool may be missing
When the agent says it cannot do something it used to, check in this order:

1. **Settings → Tools** — did you toggle it off in this browser?
2. **Disabled by the admin** — the tool has vanished for everyone; the agent
   reports *"tool X has been disabled by the administrator on this platform"*.
3. **Admins only / group-restricted** — the agent reports *"permission denied:
   tool X is restricted"*. Ask your group admin or a global admin.
4. **MCP server disabled or unreachable** — its tools are withdrawn until the
   server is enabled and connected again (see the MCP help).
5. **App hidden by the admin** — an app switched off in **Admin console →
   Platform** (e.g. Email) disappears from the rail, the palette and Settings.

Blocked calls are recorded in the audit trail (**Admin console → Usage**), so
an admin can see what was refused and why.

### Group tool access

In shared mode a group admin can tighten the global policy for their own group from the admin console → **Group tool access**: a tool that is open platform-wide can be restricted to that group's admins. It only ever tightens — a tool locked globally stays admin-only, and a group admin cannot loosen it.
