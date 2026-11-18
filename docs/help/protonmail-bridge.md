# Email → Proton Mail Bridge

Proton Mail Bridge connects to your Proton account and provides the IMAP/SMTP
servers used by Prism. A compatible paid Proton Mail plan is required. Prism's
standard Docker Compose includes Bridge as the optional `protonmail-bridge`
service; it is not necessarily running or connected to an account.

## Start a guided setup

For a request such as “help me connect my Proton Mail account”, explain the three
stages: sign in to Proton in the temporary Bridge console, leave that console
and start the permanent Bridge service, then connect Prism using Bridge's
generated mail-client credentials. Establish whether the user has
already connected Bridge and which installation/terminal they are using. Give
the next commands with their execution location, not just `login` on its own.
An initialized Bridge can have no connected account: the first Proton sign-in
does not necessarily require a new keyring. Reopen its existing CLI in that case.

**The temporary console is not the permanent mail endpoint.** While the service
is stopped, its `protonmail-bridge` hostname may not resolve from Prism. A Compose
`run` container does not normally get the service's network alias, and opening
the binary with `--entrypoint ... --cli` skips this image's 143/25 port forwarders.
After `login` and `info`, guide the user through `exit`, starting the permanent
service and checking its state **before** configuring or testing Prism. Account
sign-in, a listening socket, and a successful Prism IMAP login are separate checks.

`prism_help` without a topic reports saved integration settings. It does not
prove that Bridge is installed, running or authenticated. An empty `docker ps`
inside the execution workspace, or no Compose file there, does not establish
that the installation's Bridge is missing. With `DOCKER_MODE=workspace`, the
Docker tools see the inner daemon; the mail connection originates in the Prism
server and may use a service on the outer host. See `docker-backends`.

The local Bridge capability in `/api/platform` indicates whether this deployment
allows the bundled preset, not whether the service exists. Check available
facts before proposing another deployment. If the host is outside your access,
say what remains unverified and guide the user through its normal terminal.
An `exec_command` DNS/TCP check of the configured Bridge endpoint can establish
reachability from the workspace, even when its container is on the outer daemon;
this alone does not validate the account or the mail connection from Prism.

## If the user is already at a Bash prompt inside Bridge

The bundled image is managed with Compose **from the Docker host**. Return to
that host terminal before stopping it: if you entered with `docker exec`, `exit`
leaves the container's Bash shell. A web console inside a container is not a
host terminal.

For an already initialized volume, run on the host, in Prism's Compose directory:

```bash
docker compose --profile protonmail stop protonmail-bridge
docker compose --profile protonmail run --rm --no-deps --entrypoint protonmail-bridge protonmail-bridge --cli
```

This opens the existing vault in a single interactive Bridge process. Stopping
processes with `pkill` inside the running container can terminate or restart its
service; it is not the procedure for this image. It does not use `supervisorctl`.
Do not guess a vault passphrase or suggest a supposed default.

Wait for the Bridge welcome message and `>>>`, then follow the console sign-in
steps below. After leaving the console with `exit`, restart the permanent service
from the host with the command in step 3 below.
If this volume has never been initialized, use the first-setup procedure next.

## 1. Open the Bridge console on the Docker host

For a first setup with the standard Prism Compose, use a terminal **on the host
running Prism**, in the directory containing its `docker-compose.yml`:

```bash
docker compose --profile protonmail build protonmail-bridge
docker compose --profile protonmail stop protonmail-bridge
docker compose --profile protonmail run --rm --no-deps protonmail-bridge init
```

Skip the build when the Bridge image is already available. On a custom Compose
deployment, retain the same project, Compose file, environment file and volume
as the existing installation. Reuse its `-p`, `-f` and `--env-file` options for
every command; ask for them if needed rather than guessing. The commands below
use the standard installation's defaults. Do not launch a second unrelated Bridge.

Here `init` is an argument to the **Docker image's entrypoint**. It prepares
GPG and the `pass` password store, then runs `protonmail-bridge --cli`.
`protonmail-bridge init` typed inside a container is a different, incorrect
command: it can launch the graphical app and fail on missing OpenGL libraries.
Installing GUI libraries is not needed for the CLI setup.

Wait for the welcome message identifying the **Proton Mail Bridge interactive
shell**, with its `>>>` prompt (the appearance can vary by version). A shell
prompt such as `root@container:/protonmail#` or `$` is still
Linux, not Bridge. `login`, `info` and `exit` below are commands for the Bridge
console. Do not enter Proton credentials into Linux's `login` program.

## 2. Sign in inside the Bridge console

Enter these commands **one at a time**, answering the prompts before continuing:

```text
login
```

Bridge asks for the Proton account login and password, then a two-factor code
if enabled. Enter those directly in its console, never in Prism's chat.

After sign-in succeeds, enter:

```text
info
```

Read the **Username** and **Bridge password** for the account you want to connect.
The Bridge password is generated locally; it is not your Proton account password
and cannot be obtained from Proton webmail settings. Do not paste the full
`info` output into the chat because it contains credentials.

Then leave Bridge:

```text
exit
```

## 3. Start the permanent service before connecting Prism

Back in the **host terminal**, start and check the permanent service:

```bash
docker compose --profile protonmail up -d --no-deps protonmail-bridge
docker compose --profile protonmail ps protonmail-bridge
```

The service must be running. If it exits or restarts repeatedly, inspect its logs
before proceeding. The temporary `run --rm` console should now be closed. Prism
uses the permanent service's network alias and IMAP/SMTP forwarding ports; it
cannot use the stopped service just because `login` or `info` worked in the
temporary console. Do not start a second process against the same active vault.

Bridge v3.27.1 does not require initial synchronization to reach 100% before
IMAP authentication. Its local state is kept in the same volume when switching
from the console to the permanent service, which can continue synchronization.
Available messages may still be incomplete. Waiting for 100% in the temporary
console does not restore the permanent service's DNS name or ports.

The `protonmail` volume retains the account and keyring across restarts and
rebuilds. Do not use `docker compose down -v` for troubleshooting: it deletes
volumes, including Prism and Bridge data.

## 4. Connect Prism to Bridge

Open **Settings → Email → ProtonMail**. With the bundled Docker service:

| Field | Value |
|---|---|
| Email | the Username printed by Bridge's `info` |
| Password | the Bridge password printed by `info` |
| IMAP host / port | `protonmail-bridge` / `143` |
| SMTP host / port | `protonmail-bridge` / `25` |
| Security | STARTTLS, self-signed certificate accepted |

These settings apply to the bundled service on Prism's Docker network. The image
forwards ports 143/25 to Bridge's internal 1143/1025. A separate desktop Bridge
has its own credentials, ports and network accessibility; use its displayed
settings. Do not substitute `localhost`, which refers to the container making
the connection, or publish Bridge ports on the internet.

Prism stores the mail password encrypted. The agent can request the **Bridge
password** through `request_secret`, then call `email action=config` with its
`password_secret` name. It needs the Bridge username too. That operation saves
configuration; call `email action=list` to check IMAP access before announcing
that mail works. The UI and agent use the same account configuration.

## Reopen an already initialized Bridge

Stop the existing service on the host, then open the CLI against the **same
volume**, without initializing the keyring again:

```bash
docker compose --profile protonmail stop protonmail-bridge
docker compose --profile protonmail run --rm --no-deps --entrypoint protonmail-bridge protonmail-bridge --cli
```

Use `info` for an already connected account, or `login` to add one. After `exit`,
start the permanent service again with the command from step 3. Avoid running
two Bridge processes against the same vault. Opening another Bash terminal in
the running container does not stop its existing Bridge process.

## Troubleshoot at the failing step

- **A Linux prompt, Linux `login`, or a GUI/OpenGL error**: the Bridge CLI was not
  opened correctly. Return to the host-terminal procedure above.
- **Proton rejects credentials during Bridge's `login`**: this is the Proton
  account sign-in stage. Check its login, password and requested 2FA response;
  a Bridge password cannot authenticate to Proton. Read the actual error before
  attributing it to a password, subscription or account state.
- **A vault lock / another instance is running**: stop the service using the same
  vault before opening the interactive one-off container.
- **No keychain / could not create vault key**: a fresh volume needs the initial
  GPG/pass setup through the Docker entrypoint's `init` command.
- **Prism's IMAP login is rejected**: check that the account is connected in
  Bridge and use that Bridge's generated username/password. `no such user` may
  indicate an absent account or a mismatched username; it does not prove that
  setup was never attempted.
- **`lookup protonmail-bridge ... no such host` from Prism**: DNS failed before
  authentication. Check that the permanent service was restarted after the
  interactive console, in the same Compose project and on Prism's network.
  Waiting for account synchronization or changing the password cannot repair
  a missing service hostname.
- **Connection refused / timeout**: check the service, network and endpoint.
  Read the installation's Bridge logs on the host with
  `docker compose --profile protonmail logs --tail=50 protonmail-bridge`.

References: [Proton's CLI guide](https://proton.me/support/bridge-cli-guide),
[Bridge installation](https://proton.me/support/protonmail-bridge-install) and the
[Docker image's initialization procedure](https://github.com/shenxn/protonmail-bridge-docker#initialization).
Synchronization/authentication behavior: [Bridge v3.27.1 IMAP authorization](https://github.com/ProtonMail/proton-bridge/blob/v3.27.1/internal/services/imapservice/connector.go#L186-L196).
