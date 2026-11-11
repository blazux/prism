# Conversations and memory

## Sending and reconnecting

The chat keeps your text and attachments until Prism confirms receipt. A closed
connection leaves the draft available. If the connection drops after sending,
Prism checks the request on reconnect. **Retry send** uses the same request, so a
message already received does not start another task.

With PostgreSQL, the request and its receipt are recorded together before the
confirmation. After a server restart, an unfinished task is shown as interrupted;
it is not started again automatically. Without PostgreSQL, receipts are kept only
for the lifetime of the server process.

The status above the conversation distinguishes reconnection, pending receipt,
work in progress, waiting for input or approval, completion and interruption.
Switching between workspaces keeps each workspace's unsent draft in the open
page. Opening a global app uses your general assistant; the chat header shows
which conversation you are addressing.

If the model connection fails after a reply has started, Prism keeps the text
already received and reports the interruption. Tool calls from an incomplete
model response are not executed. Review the partial reply before asking to
continue; completed actions from earlier model calls are not undone.

## Reading earlier exchanges

The chat initially loads the newest 100 archive rows. **Load older messages**
reads another page without clearing the current response. An exchange can have
several rows for its messages and tool calls. Tool results and downloadable
attachments remain part of the archive.

The archive displayed in the browser is separate from the context sent to the
model. The agent uses a bounded recent history and summaries of older material.
Loading an older page does not add it to the model's next request.

The agent can use `search_history` with `query` to find earlier discussions.
Results include the session and message ID. To read one result in full, it can
use the same tool with `session` and `message_id` copied from the result. The
`text` field contains the original serialized message, including any recorded
tool calls. If `nextOffset` is not null, read the same message again with
`offset=nextOffset` until it is complete.
For a conversation, it can read a page with `session`, then follow the returned
`before` cursor while `hasMore` is true. Large tool results still have output
limits; read a smaller page when needed.

In shared mode, personal searches cover that user's workspaces and channels.
A shared agent searches its group's Room, Webex and Teams conversations. Reading
a search result applies the same boundary; it does not grant access to another
user or group.

## Reviewing what the agent remembers

Open **Settings → Knowledge & memory → Memory**:

- **Profile facts** are personal facts such as your location or preferences.
  They are shared across your own workspaces, not with other group members.
- **Learned facts** contain saved lessons. The agent retrieves relevant lessons
  by similarity; they are not all included in every response.

Expand a saved fact to read, correct or delete it. Corrections use the configured
embedding provider. If indexing fails, the previous fact is kept. If the stored
text changed since you opened it, reload it before saving your correction.

**Documents** contains the knowledge base available to you. In shared mode,
the group administrator manages group documents. **Skills** contains the reusable
procedures saved for your agent: open one to read or edit its steps. Skills are
personal in multi-user mode; shared agents have their own group library.

Open **Summary** in the chat header to read or correct that conversation's
context summary. Save a correction after its task finishes. Saving keeps the
archive and the boundary between summarized and recent messages. If Prism also
keeps an earlier history summary, it appears separately in the same panel.
Changing conversations closes the panel; unsaved corrections stay with their
original conversation while the page is open.

Settings apply across workspaces. MCP connections belong to the installation
in single-user mode and to the group in shared mode. Opening Settings from a
different workspace does not select another set of connections or memories.

Editing a fact, skill or summary does not erase its earlier appearances in
conversation history. **Clear chat** clears that conversation's messages and
summary; it does not delete your saved profile, facts or skills.
