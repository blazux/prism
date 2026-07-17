# Email → ProtonMail (Bridge)

Proton doesn't speak IMAP. Proton Mail Bridge is a local process that logs into
your Proton account, decrypts your mail, and re-serves it as ordinary IMAP/SMTP —
and that is what Prism connects to. It ships as the bundled `protonmail-bridge`
container, so there is nothing to install.

**Bridge requires a paid Proton plan.** It does not exist on free accounts.

## The thing that trips everyone up

The password Prism needs is **not** your Proton password, and it is not hiding
somewhere in your Proton account settings. The Bridge **generates** it, locally,
once the Bridge itself is logged into Proton. Before step 1 there is simply no
password to find — and the Bridge answers every login attempt with `no such user`.

## 1. Log the Bridge into Proton (once)

From the folder holding `docker-compose.yml`:

```bash
docker compose stop protonmail-bridge      # it holds the /root volume that the
                                           # next command needs
docker compose run --rm protonmail-bridge init
```

`init` generates a GPG key and initialises the `pass` store before opening the
Bridge shell. That step is mandatory, not decoration: without it the Bridge cannot
create its vault and dies with `Could not load/create vault key: no keychain`.

At the Bridge prompt, three commands:

```
login     # your Proton address, your password, then the 2FA code
info      # prints Username + Bridge password  ← the one Prism needs
exit
```

Copy what `info` prints. You can come back and read it again at any time by
re-running the same `init` command and typing `info`.

## 2. Put the Bridge back in service

```bash
docker compose up -d protonmail-bridge
```

The session lives in the `protonmail` Docker volume, so it survives restarts,
rebuilds and reboots. It does **not** survive `docker compose down -v`: that
deletes the volume, and you log in again from scratch, 2FA included.

## 3. Configure Prism

**Settings → Email → ProtonMail** — the preset already fills in the Bridge's
coordinates:

| Field | Value |
|---|---|
| Email | the **Username** printed by `info` |
| Password | the **Bridge password** printed by `info` |
| IMAP host / port | `protonmail-bridge` / **143** |
| SMTP host / port | `protonmail-bridge` / **25** |
| Security | STARTTLS, self-signed certificate accepted |

The host is the container name: Prism reaches it over the Docker network, not over
the internet. The Bridge itself listens on 1143/1025 and the container re-exposes
those as the standard 143/25 — which is why the ports look unremarkable.

Prism stores the password AES-encrypted under the `email_password` secret, so both
the UI and the agent use the same account.

## When it doesn't work

- **`no such user` in the Bridge logs** — the Bridge has no Proton account. Step 1
  was never done, or its volume was wiped. Whatever you type into Prism is
  irrelevant until the Bridge itself is logged in.
- **`Incorrect login credentials`** — you are almost certainly using your Proton
  password. Prism wants the Bridge password from `info`.
- **Nothing works at all and your plan is free** — expected. Bridge is paid-only.
- Watch it happen live while you save the settings:
  `docker logs -f prism-protonmail-bridge`. A working connection logs an IMAP
  login instead of a rejection.
