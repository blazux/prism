# Activity

Activity opens on **Agent work**, which shows the latest recorded work in each
accessible conversation, including personal workspaces, the general assistant,
Telegram and Rooms.

- **Needs your attention** contains requests waiting for input or approval and
  work that failed, timed out or was interrupted.
- **In progress** contains active work and queued Room requests.
- **Finished** contains the latest completed or deliberately stopped work in
  those conversations, with its recorded date when known.

Each card shows the initiating request when known, its conversation and current
state. Start, finish and last-event times appear when recorded; old entries may
have no duration. A last-event time is an observation, not proof that an agent
is stuck. Open the conversation to read the result or handle a pending request.
Opening it does not start or resume an agent turn.

Tool and delegated-worker details are available within the card. Background
agent turns are counted separately from direct widget or script tool calls.
The view refreshes every five seconds while visible. It shows the latest work
per conversation; earlier work remains in that conversation and the journal.
A completed state means the turn ended without a reported error, not that
every requested outcome has been independently checked.

**Actions** shows completed changes made through tools,
errors, webhook executions and inbox rule results. A task update shows its action and
available title/id; an email rule shows how many messages it processed or why
it failed. Quiet reads and polling stay out of this view.

Agent work follows your own workspace and Room permissions; the **everyone** audit
filter does not extend it. Workspace badges and Work indicate agent
conversations. A widget or
scheduled script calling a tool directly does not mark the agent as working.

An **interrupted** card identifies work stopped by a server interruption,
including conversations started from Telegram. Open its saved conversation and review the tool results,
then choose **Resume** or send a follow-up. Resume sends a normal continuation
message; it preserves any separate draft and attachments in the composer.
Reconnecting or opening the conversation does not resume it automatically. A
tool without a saved result may already have changed something: the agent must
check the current state before repeating it.

**Details** offers **Tools**, **Chat**, **Errors** and **Webhooks** for the technical
history when investigating a problem. Tool entries show whether the call completed or failed
and how long it took. Refresh or load older entries as needed.

This is an operation journal, not a transcript or a guarantee that every shell
command has been understood. Full tool arguments and results remain in the chat;
Activity does not copy passwords, email bodies, recipient lists or arbitrary
shell commands into its summaries. Older entries created before action details
were recorded keep their original information.

In shared mode users retain their existing visibility scope. Activity does not
expand access to another member's history. The agent can explain this screen
through `prism_help topic=activity`.
