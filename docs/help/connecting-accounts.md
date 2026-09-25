# Connecting your accounts (no OAuth)

These connections use a password or token — no OAuth dance. For Google and
Microsoft, see their dedicated guides.

## Email → IMAP/SMTP
**Settings → Email**: pick your provider and Prism fills in the servers for you.
Nearly every provider now refuses your normal password and wants an **app
password** instead:

- Gmail: turn on 2-Step Verification, then **Google Account → Security → App
  passwords**.
- Outlook.com / Hotmail requires OAuth2, which the email client does not yet implement. Do not suggest an app password as a workaround. Microsoft Calendar OAuth is separate.
- iCloud: **appleid.apple.com → Sign-In and Security**.
- Yahoo: **Account Security**.

**ProtonMail is the exception**: there is no app password to go and fetch, because
Proton has no IMAP at all. A local Bridge container logs into Proton and *generates*
the password Prism uses — see the dedicated ProtonMail guide. If the Bridge logs
say `no such user`, the Bridge itself isn't logged in yet, and nothing you type in
Prism will help.

## Notes → Obsidian / Logseq vault
A vault is just a folder of Markdown files, so Prism reads and writes it directly.
This source is available for self-hosted Prism only. Hosted personal environments
use the personal Prism database and reject local vault paths, including through
the API and agent tools. A path on the user's computer is not a server mount.
Workspace files and synchronization tools are separate capabilities; they do not
connect a folder to the Notes app automatically.

1. In `.env`, set `NOTES_VAULT_PATH=/absolute/path/to/MyVault` to an existing
   folder on the Docker host. Run `docker compose up -d --no-deps prism-server`
   to recreate the server with the mount (a restart alone does not update mounts).
2. In Prism: **Settings → Notes → Source → Markdown vault**, and set the folder
   to `/vault` (the path *inside* the container). Save.

A note's title is its filename; the body is the file's content (frontmatter and
`[[wikilinks]]` are preserved). Edit notes in Prism or in your own editor — both
ways. Tags are read from YAML frontmatter.

## Calendar & Tasks → CalDAV
Works with Apple iCloud, Nextcloud, Fastmail, mailbox.org, Zoho and most
self-hosted servers, using an **app-specific password** (not your main password).

Common server URLs:
- Apple iCloud: `https://caldav.icloud.com`
- Nextcloud: `https://YOUR-HOST/remote.php/dav`
- Fastmail: `https://caldav.fastmail.com`

Steps:
1. Create an app-specific password with your provider (e.g. Apple ID →
   Sign-In & Security → App-Specific Passwords).
2. In Prism: **Settings → Calendar**, enter the server URL, your username/email,
   and the app password. Click **Connect**.
3. Prism discovers your calendars. Pick which one holds **events** and which
   holds **tasks**, then Save selection.

## Choosing the active source
You can connect several providers. **Settings → Calendar → Active sources** lets
you pick which one each app uses: a **Calendar (events)** selector (Auto, Local,
CalDAV, Google) and a **Tasks** selector (Auto, Local, CalDAV, Todoist). Only
connected providers appear. **Auto** uses the best connected one (for events:
Google → CalDAV → local; for tasks: Todoist → CalDAV → local) and shows what it
currently resolves to. So if events still show CalDAV after connecting Google,
either leave it on Auto or pick **Google** explicitly here.

## Tasks → Todoist
1. In Todoist: **Settings → Integrations → Developer**, copy your **API token**.
2. In Prism: **Settings → Calendar → Tasks via Todoist**, paste the token,
   Connect. When connected, the Tasks app uses Todoist instead of CalDAV/Prism.
   (Todoist's API only lists active tasks, so completed ones won't appear.)

## Asking the agent

The agent can connect CalDAV and Todoist for you, and point notes at a Markdown vault. Passwords and tokens never go through the chat: the agent first asks for them with its secret dialog (they are stored as secrets), then uses the secret to connect — and it tests the connection before saving anything. It can also switch the active calendar/tasks source ("use Todoist for my tasks"). Google and Microsoft accounts still need the browser sign-in from Settings → Calendar; the agent will guide you there.

## Hosted personal deployments

Settings adapt to the capabilities returned by `/api/platform`. The agent also
receives the deployment limitations in its integration status. Local vaults and
the bundled `protonmail-bridge` preset are unavailable in hosted environments;
self-hosted Prism retains them. Custom public mail endpoints remain configurable.
A Docker Bridge inside a workspace is not automatically reachable by the mail
client: its IMAP/SMTP connections currently originate in the application server,
not the workspace. Do not present that as a tested setup recipe.

Webhook URLs must be used exactly as returned by Prism (UI or webhook tool).
Hosted URLs route to their owner without a dashboard session and still require
the webhook token. Suspension, disabled/deleted hooks and rotated tokens deny
access. Hosted webhook execution is bounded; callers receiving HTTP 429 should
retry with backoff. No additional tool arguments are required.
