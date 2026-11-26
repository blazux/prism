# Email

For help connecting a mailbox, first read `prism_help` with topic
`connecting-accounts`. For **Proton Mail**, read `protonmail-bridge` before
requesting credentials or configuring Email: the user first signs in through
Bridge's interactive console, closes it and starts the permanent service, then
Prism uses the credentials Bridge generates. The temporary console does not
provide the permanent service's standard hostname and forwarded ports.
Explain how to open that console from the Docker host before asking them to type
`login`; a Bash terminal is not the Bridge console.

The `email` tool's `config` action **reads** saved non-secret settings when called
without configuration fields. Supplying connection fields saves those settings.
The read reports `configured`, `has_password` and `connection_tested:false`;
it does not contact the mail server. Complete the provider's prerequisites
before collecting the mail-client password. Use `email action=list` to validate
IMAP access after setup. `prism_help` without a topic also reports saved
integration status.

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
**Unread** searches the whole selected folder before displaying a page of results.
Use the bottom arrows for older or newer results, including while searching.
Search is within the selected folder. Categories and tags filter the loaded
page; they remain separate from actual IMAP folders.

Select messages using their checkboxes, or use **This page** to select the
visible messages. The selection toolbar can archive, move, send to Trash or
mark several messages read or unread. For example, select twenty newsletters
and archive them together. The progress report distinguishes confirmed changes,
changes that could not be confirmed and messages not processed. Successful
messages leave the selection; review failures before trying them again.
**Stop** stops starting new operations and waits for those already in progress.
Finish or stop the operation before leaving Email.

While Email is visible, it checks for mailbox changes approximately every
30 seconds. New messages refresh the list without closing the message you are
reading or resetting its scroll position. Automatic checks pause while a draft
is open or selected messages are being processed. **Refresh** reloads the
current folder or search results immediately. If refreshing fails, the current
message remains readable and the app displays the error.

If Archive/Trash is not advertised and has no recognized name, choose the
folder explicitly with Move. A server without MOVE or UIDPLUS cannot safely
move individual messages through Prism; the error explains this limitation.

## Prism rules

These rules are saved and executed by Prism. Filters configured with your mail
provider, such as Proton Mail or Gmail, are managed separately and are not
synchronized with this list.

**Prism rules → + Rule** creates a named rule with one condition and one action:
- Match messages whose From, To or Subject field **contains** the specified text. Matching is case-insensitive.
- Choose whether matching messages should move to a selected folder or be marked as read.

**Preview matches** shows matching inbox mail without changing it. **Save**
persists the rule. Enabled rules run approximately every minute while Prism is
running, even with the dashboard closed, without a model call. They also apply
to existing matching inbox mail. Disable the checkbox to save a paused rule.
Rules run in list order; a moved message is no longer in INBOX for subsequent
rules. A mark-read rule ignores messages already read. Each pass handles up to
100 messages per rule; later passes continue the remaining work.

Edit, pause, enable or delete a rule from the list. **Run enabled rules now**
applies them immediately and reports results, including any server errors.
Activity shows rule applications and failures. Prism must remain running to
execute these rules.

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

**New message** opens the composer with To, Cc, Bcc, subject, message and
attachments. Bcc recipients receive the message without appearing in its
recipient headers. A plain-text signature can be configured in **Settings →
Email**; it is inserted into new drafts, where you and the agent can edit it.

Drafts are saved in Prism, including their attachments. They belong to your
account and are available across your workspaces. The save indicator shows
whether your latest edits have reached the server; keep the composer open if
saving fails. Open **Prism drafts** to continue a saved message after closing
the app, reloading the page or restarting Prism. These drafts are separate from
your mailbox's IMAP Drafts folder and do not synchronize to other mail clients.

**Save & close** keeps the draft; use **Delete draft** to remove it explicitly.
If another tab changes a draft, Prism refuses to overwrite that saved version
and keeps your current text visible. Review both versions before continuing.
A failed send keeps the draft. If the connection fails during sending, check
your mailbox before retrying: the server may already have accepted the message.
Reply and Reply all include the original message as quoted text below your
answer. They use the message's Reply-To address when one is provided. The list
sorts by the actual timestamp, regardless of the sender's timezone.

Forward includes the original attachments. Prism downloads them before saving
or sending the forwarded draft. If a download fails, your text stays in the
composer and sending is blocked. Use **Retry attachments**, or explicitly
choose to continue without the original attachments. Your own added files are
kept. Saved drafts and their attachments remain subject to the 20 MB limit.

If the connected mailbox changes in another tab, Prism refuses actions that
still refer to the previous mailbox. Refresh Email before acting on the new
mailbox. A saved draft from the previous mailbox remains available; use its
explicit copy action if you want to prepare it for the current account.

## Writing together with the agent

With Mail open, ask the agent to prepare a reply or a new message; it can open
the composer itself. You can also open New message or Reply, then ask in chat,
for example “make this more concise” or “suggest a subject”. The composer becomes the active context, including its
current unsaved text. Closing it restores the context of the message or folder.
To open a draft, use `editor action=open app=email`; include the original
`folder` and numeric `uid` for a reply (folder defaults to INBOX), or omit uid
for a new message. This returns the draft and revision. It will not replace
an existing composer or a draft opened while the message is loading. Mail must
already be the active app in the browser connected to this chat.

For an existing draft, use `editor action=read`, then `editor action=update revision=...`
with only the changed `to`, `cc`, `bcc`, `subject` or `body` fields. Changes
appear directly in the composer and are saved as a Prism draft. This never
sends mail. A save failure is reported; do not describe the draft as saved
until the update succeeds.
When replacing a reply body, keep the quoted original below the answer unless
the user wants it removed.
For a long body, follow `next_offset` while `truncated=true` before rewriting it.
If the user types or changes documents after the read, an update is rejected;
read again and reconsider the change. Do not retry a stale replacement blindly.
The editor tool acts only on the browser tab connected to this chat; it is not
available from a headless API call, messaging channel or closed app.

The `email` tool's `send` and `reply` actions also accept `cc` and `bcc`.
Their `body` is sent as supplied: include any desired signature there yourself.
Do not send a message merely because the user requested a draft.

## Saved drafts from another channel

When no browser editor is open, use the saved-draft API through `exec_command`
with the existing `PRISM_URL` and `PRISM_TOKEN` environment variables. Keep the
token in the authorization header, never in a link or user-facing output.

- `GET /api/email/drafts` lists your draft summaries; add `?id=ID` to read a
  complete draft, including attachments.
- `POST /api/email/drafts` saves `to`, `cc`, `bcc`, `subject`, `body`, `folder`
  and optional reply `uid`. For a new draft, send a fresh UUID as `client_id`;
  for an edit, pass the saved `id` and `revision`. Copy these values from the
  response. Omit `attachments` to preserve existing files; an empty array
  removes them. Attachment objects use `filename`, `content_type` and base64
  `data`, with a combined decoded limit of 20 MiB.
- `DELETE /api/email/drafts?id=ID&revision=REVISION` removes that exact saved
  version. It does not delete a newer revision from another editor.

On a conflict, read the saved version and reconcile it with the user's intended
changes. A response with `replayed: true` means an earlier create request already
arrived: compare that draft with your intended text before treating it as saved.
Saving a draft never sends it. Only when the user has requested sending, use
`POST /api/email/send` with `draft_id` and `draft_revision` to send the saved
content. A draft whose delivery is unconfirmed must be reviewed against Sent
before creating and sending a copy.

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
