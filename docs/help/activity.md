# Activity

Activity opens on **Actions**: completed changes made through tools, errors,
webhook executions and inbox rule results. A task update shows its action and
available title/id; an email rule shows how many messages it processed or why
it failed. Quiet reads and polling stay out of this view.

**Details**, **Tools**, **Chat**, **Errors** and **Webhooks** expose the technical
history when investigating a problem. Tool entries include completion/failure
and duration. Refresh or load older entries as needed.

This is an operation journal, not a transcript or a guarantee that every shell
command has been understood. Full tool arguments/results remain in the chat;
Activity does not copy passwords, email bodies, recipient lists or arbitrary
shell commands into its summaries. Older entries created before action details
were recorded keep their original information.

In shared mode users retain their existing visibility scope. Activity does not
expand access to another member's history. The agent can explain this screen
through `prism_help topic=activity`.
