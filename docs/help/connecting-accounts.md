# Connecting your accounts (no OAuth)

These connections use a password or token — no OAuth dance. For Google and
Microsoft, see their dedicated guides.

## Email → IMAP/SMTP
**Settings → Email**: pick your provider and Prism fills in the servers for you.
Nearly every provider now refuses your normal password and wants an **app
password** instead:

- Gmail: turn on 2-Step Verification, then **Google Account → Security → App
  passwords**.
- Outlook / Hotmail: **account.microsoft.com → Security** (needed when 2FA is on).
- iCloud: **appleid.apple.com → Sign-In and Security**.
- Yahoo: **Account Security**.

**ProtonMail is the exception**: there is no app password to go and fetch, because
Proton has no IMAP at all. A local Bridge container logs into Proton and *generates*
the password Prism uses — see the dedicated ProtonMail guide. If the Bridge logs
say `no such user`, the Bridge itself isn't logged in yet, and nothing you type in
Prism will help.

## Notes → Obsidian / Logseq vault
A vault is just a folder of Markdown files, so Prism reads and writes it directly.

1. Make the vault folder reachable by the **server container**. In
   `docker-compose.yml`, mount it under prism-server, e.g.
   `- /path/to/MyVault:/vault`, then restart.
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
