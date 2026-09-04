# Secrets

A secret is a named value (API key, password, token) stored **encrypted** in
Prism's database and handed to the agent's scripts as an environment variable —
without ever appearing in the chat. Secrets need Postgres; without it the tab
shows *Indisponible (Postgres requis)*.

## Settings → Secrets
- **Add a secret**: a **Name** (e.g. `MY_API_KEY`) and a **Value**, then
  **Save**. Each row shows the name and the environment variable it becomes
  (`$MY_API_KEY`). Values are never shown again; **✕** deletes.
- The env var name is the secret name uppercased, with anything that is not a
  letter or digit turned into `_` (`openai_key` → `$OPENAI_KEY`).

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
group ones included) or to delete one.

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
