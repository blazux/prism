# Secrets

A secret is a named value (API key, password, token) stored **encrypted** in
Prism's database and handed to the agent's scripts as an environment variable —
without sending the entered value through the chat. Secrets need Postgres; without it the tab
shows *Indisponible (Postgres requis)*.

## Settings → Secrets
- **Add a secret**: a **Name** (e.g. `MY_API_KEY`) and a **Value**, then
  **Save**. Each row shows the name and the environment variable it becomes
  (`$MY_API_KEY`). Values are never shown again; **✕** deletes.
- The env var name is the secret name uppercased, with anything that is not a
  letter or digit turned into `_` (`openai_key` → `$OPENAI_KEY`). Use a name
  such as `MY_API_KEY`, and reuse its exact spelling. Prism refuses two different
  names mapping to the same variable (`my-key` and `my_key`, for example).
  `PRISM_*` variables and built-in integration names are reserved.

In a shared deployment the tab has two sections:

- **Personal secrets** — yours only. No other member, and not the group's
  shared agent, can see or use them.
- **Group secrets** — shared with the whole group: every member's agent gets
  them (a common account's login, an API key for a group MCP server). Tick
  **Group secret — shared, usable by every member** when adding one (pick the
  group if you belong to several). Any member may add or delete a group secret;
  deleting one removes it for everyone.
- **⇧ Share with group** on a personal secret *moves* it to the group's list
  (the value is never read back into the browser). It leaves your personal
  list, so a stale personal copy can never shadow the group's value later.

Group admins also manage group secrets from the **Admin console → Secrets**
pane (and the secret picker in **Admin console → MCP** can create one inline).

## When the agent asks for a secret
If a task needs a credential the agent doesn't have, it calls `request_secret`
and the dashboard opens a **Secret requis** dialog: a description of what is
being asked, a password field, **Annuler** / **Confirmer** (Enter confirms,
Escape cancels). The value goes straight from the dialog to the server and is
stored under the name the agent chose — it never transits through the chat
transcript, and the agent only learns *that* it is now stored. If a secret of
that name already exists (personal, or shared by your group), the agent is told
so and no dialog appears.

The dialog exists in the dashboard chat only. Over Telegram, Webex, webhooks or
cron, `request_secret` is unavailable — add the secret in Settings first.

You can also ask the agent to *list* your secrets (names only, never values,
group ones included), to delete a personal one, or to **share one with your
group** ("share my `github_token` with the team"): the same move as the ⇧
button — the value goes from your scope to the group's without ever passing
through the chat, and your personal copy is removed. To create a group secret
from scratch, ask for it as usual (the secure dialog stores it as yours), then
ask to share it.

## How scripts get them
Every `exec_command` and custom tool run receives your usable secrets as
environment variables: the group's shared tier first, overlaid by your own
scope, so a personal secret with the same name wins. In Python that is
`os.environ['MY_API_KEY']`, in shell `$MY_API_KEY`.

## The cron exception
**Cron jobs do not receive secret env vars.** A script that reads
`os.environ['MY_SECRET']` works when the agent runs it in chat and silently
fails under cron. A script destined for cron must fetch its secrets over HTTP
instead (this works in both contexts):

```bash
curl -s "$PRISM_URL/api/user/secrets/<name>?session=$PRISM_SESSION" \
     -H "Authorization: Bearer $PRISM_TOKEN"
```

The `?session=` part is what tells the server whose secrets to read — your
personal ones first, then your group's shared ones (a shared-agent job resolves
*its* group the same way). Without it, group secrets are not found. The agent
knows this rule; if a cron job "can't find" a secret, this is the first thing
to check.

## Reserved integration credentials
Some names are reserved for Prism's own integrations: `email_password`,
`caldav_password`, `todoist_token`, `telegram_bot_token`, `slack_bot_token`,
`slack_app_token`, the Webex bot tokens and MCP OAuth tokens. They are stored
with the same mechanism but are **never** injected into scripts, never served
by the HTTP endpoint above, cannot be used as an MCP server's bearer token, and
cannot be created as or shared to a group secret by a plain member. Manage
them from their own tabs (Email, Calendar, Channels) or, for a group, from the
Admin console.

## Errors and local backups
If loading or decrypting secrets fails, Prism stops the script and reports the
error instead of running with missing credentials. Existing conflicting names
must be removed or recreated under distinct names in Settings → Secrets.
No existing secrets are renamed automatically.

Back up the database together with the workspace's `.secret_key`, and protect
that backup: both are required to restore encrypted credentials. Never delete
or replace `.secret_key` to resolve a read error; restore access to the original
file or restore its backup.

Scripts receive usable credentials, so do not print environment variables,
secret values, or authorization headers. The secure input dialog avoids putting
the value into chat; encryption at rest does not stop a script from exposing
it in its output.
