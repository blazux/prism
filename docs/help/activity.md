# Activity

Activity opens on **Actions**, which shows completed changes made through tools,
errors, webhook executions and inbox rule results. A task update shows its action and
available title/id; an email rule shows how many messages it processed or why
it failed. Quiet reads and polling stay out of this view.

**In progress** shows active tasks in your personal conversations, including
your general assistant and Telegram, and work in Rooms you can access. It shows
the current request, recent tools, subagents, background jobs and any queued
Room messages. This view
refreshes every five seconds while visible. It follows your own workspace and
Room permissions; the **everyone** audit filter does not extend this view.
Workspace badges and **In progress** indicate agent conversations. A widget or
scheduled script calling a tool directly does not mark the agent as working.

**Interrupted** lists your personal conversations whose task stopped when the
server stopped, including conversations started from Telegram. **Open chat**
opens the saved conversation without starting any work. Review its tool results,
then choose **Resume** or send a follow-up. Resume sends a normal continuation
message; it preserves any separate draft and attachments in the composer.
Reconnecting or opening the conversation does not resume it automatically. A
tool without a saved result may already have changed something: the agent must
check the current state before repeating it.

**Details**, **Tools**, **Chat**, **Errors** and **Webhooks** expose the technical
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
