# Email: folders and inbox rules

Connect your IMAP/SMTP account in **Settings → Email**, then open **Email**.
The left sidebar lists your server's folders, including Sent, Archive and
Trash when the server provides them. On small screens, the top-left menu opens
the sidebar. The settings icon next to **Folders** opens folder management,
where you can create, rename or delete a folder. Deleting requires an empty folder with no subfolders; INBOX and folders
advertised as system folders cannot be deleted here.

Open a message to read, reply, reply all, forward or download attachments.
**Move** selects a destination; **Archive** and **Trash** move to the matching
server folder. Trash is a recoverable move: Prism does not permanently expunge
all mail. To restore a message, open Trash and move it back. The message’s **More (…) → Mark unread** keeps it as a reminder.
**All / Unread** filters the loaded page; use the bottom arrows for older or newer messages. Search is within the selected folder; **Older messages**
and **Newer messages** browse the folder's pages. AI categories remain separate
from actual IMAP folders.

If Archive/Trash is not advertised and has no recognized name, choose the
folder explicitly with Move. A server without MOVE or UIDPLUS cannot safely
move individual messages through Prism; the error explains this limitation.

## Rules

**Inbox rules → + Rule** creates a named rule with one condition and one action:
- From, To or Subject **contains** text (case-insensitive).
- Move to a chosen folder, or mark read.

**Preview matches** shows matching inbox mail without changing it. **Save**
persists the rule. Enabled rules run approximately every minute while Prism is
running, even with the dashboard closed, without a model call. They also apply
to existing matching inbox mail. Disable the checkbox to save a paused rule.
Rules run in list order; a moved message is no longer in INBOX for subsequent
rules. A mark-read rule ignores messages already read. Each pass handles up to
100 messages per rule; later passes continue the remaining work.

Edit, pause, enable or delete a rule from the list. **Run enabled rules now**
applies them immediately and reports results, including any server errors.
Activity shows rule applications and failures. These are Prism rules, not
Sieve/Gmail rules installed at your mail provider: Prism must remain running.

## Ask the agent

You can say “create a folder for invoices”, “move this message to Projects”,
“mark this unread”, or “put messages from news@example.org in Newsletters”.
The agent can explain the steps or perform them using the `email` tool.

For the agent:
- `email action=folders` discovers exact server names.
- `email action=list folder=Projects` returns messages; **keep folder and uid
  together**. UIDs are only meaningful inside their source folder and may change
  after a move. List the destination again rather than reusing the old UID.
- `email action=move folder=INBOX uid=42 target=Projects` moves one message.
- `email action=create_folder folder=Invoices`; rename uses `target` for the new
  name; delete requires an empty folder.
- `email action=rule_preview name=News rule_field=from
  rule_contains=news@example.org rule_action=move target=Newsletters` previews
  a proposed rule. Create the destination first if it does not exist.
- `email action=rule_save` with those same fields saves it; `enabled=false`
  pauses it. Saving an existing name changes only supplied fields.
- `email action=rules` lists rules; `rule_delete name=News` removes one;
  `rules_apply` runs enabled rules. A named `rule_preview` can inspect a saved,
  paused rule without enabling it.

Folder renames automatically update rule destinations, including subfolders and
paused rules. A folder used by a rule cannot be deleted: change that rule’s
destination or delete the rule first. Pausing it is not enough. Mailbox provider limitations
are reported as errors, not simulated successes.

## Reading and writing

The list stays beside the message on wide screens. On a phone, use the back
arrow to return to the list. Search is in the top bar; **Clear search** returns
to the folder. The message toolbar offers Archive, Move, Trash, Categorize and Summarize.
Reply, Reply all and Draft reply are below the sender, followed by Create task
and Add to calendar. Forward and Mark unread are in **More (…)**.

**New message** opens the composer. Recipient, subject and message are separate
fields, with attachments and Send at the bottom. Failed sends keep your draft;
closing an edited draft asks before discarding it. Drafts in this composer are
not saved to the server automatically.

## Writing together with the agent

Open New message or Reply, then ask in chat, for example “make this more concise”
or “suggest a subject”. The composer becomes the active context, including its
current unsaved text. Closing it restores the context of the message or folder.
The agent uses `editor action=read`, then `editor action=update revision=...`
with only the changed `to`, `subject` or `body` fields. Changes appear directly
in the composer. This does not send mail and does not store a server-side draft.
For a long body, follow `next_offset` while `truncated=true` before rewriting it.
If the user types or changes documents after the read, an update is rejected;
read again and reconsider the change. Do not retry a stale replacement blindly.
The editor tool acts only on the browser tab connected to this chat; it is not
available from a headless API call, messaging channel or closed app.

## Visible agent actions

Beside **Filters** in the top bar, **Categorize all** processes uncategorized mail
throughout the current folder, including older pages. Already categorized mail
is skipped. Work is saved in batches of at most 20 messages; progress is shown
and **Stop** stops after the current batch. If interrupted, run it again to
continue. Categorization adds categories and tags; it does not move messages.
**Summarize unread** asks the chat agent about unread mail in this folder.

In an opened message, **Categorize** categorizes that message alone,
even if it was already categorized. Summarize and Draft reply work directly
in the mail app. Create task and Add to calendar pass the message folder and
UID to the chat agent so it can act on the correct message.

## Reading long messages with the agent

`email action=read folder=... uid=...` returns readable text, retaining useful
links, instead of HTML styles and layout. The mail interface still displays
the original HTML. Plain-text messages are preserved.

If `truncated` is true, call `read` again with the same folder and UID and
`offset` set to the returned `next_offset`. Repeat until `truncated` is false.
Offsets count text characters; `total_chars` is the full readable text length.
Do not infer that a long article is missing until its continuation pages have
been read. Conversion does not fetch remote links, images or attachments.

Categories and tags are available under **Filters**, next to the top search
bar. They filter the loaded page. The button shows an indicator when a filter
is active; open it and choose **Clear** to show all categories again. More
tags and tag search remain available inside this panel.

For creation, enter a simple name such as `Invoices`: Prism adds the personal
folder prefix when the server requires one (for example `Folders/` on Proton
Bridge). A simple rename keeps the current parent folder. Explicit server paths
remain accepted. The tool returns the actual `folder` path; use that value for
subsequent reads, moves and rule destinations.
