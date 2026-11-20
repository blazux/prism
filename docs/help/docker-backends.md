# Docker backends

Prism's Docker tools keep the same names and parameters. The deployment chooses
where services run; use the URLs returned by the tools rather than guessing.

## Installation and execution are separate

The standard installation uses one `docker-compose.yml` in both personal and
multi-user mode. `MULTI_USER` controls accounts and groups; `DOCKER_MODE` selects
the daemon used by the agent's Docker tools. Neither chooses the AI provider.
The `workspace` service runs code and custom tools; the conversation loop runs
in `prism-server`. The GPU workspace variant provides PyTorch/CUDA for executed
code, not a server for the configured chat model.

Installation commands belong in a terminal on the Docker host, from the clone's
root directory. That directory is distinct from the agent's `/workspace` volume.
Use the installation's existing project, files and environment options. The
repository README describes published-image and local-build commands; do not
infer the active installation from files visible inside `/workspace`.

The normal CPU installation starts with `docker compose pull`, followed by
`docker compose up -d --no-build`. These commands use published images: `:latest`
tracks stable releases, while the pipeline publishes `main` builds as
`:unstable`. To run source changes from the checkout, use
`docker compose up -d --build`; `git pull` alone does not deploy them.

`WORKSPACE` selects the execution image, not the installation mode. Unset or
empty uses the published CPU workspace, available for AMD64 and ARM64.
`WORKSPACE=gpu` selects `Dockerfile.agent.gpu` and a separate local
`blazux/prism-workspace:gpu` tag. Other values, including `cpu` and `false`, are
unsupported.

A GPU workspace needs a compatible NVIDIA GPU and driver on the Docker host,
NVIDIA Container Toolkit configured for Docker, and a local
`docker-compose.override.yml` that grants the workspace NVIDIA devices and sets
`pull_policy: build`. Merge this configuration into existing overrides; do not
overwrite them. The README provides the complete fragment. With that policy,
`docker compose up -d` builds the GPU workspace locally. For updates with a
published server, pull `prism-server postgres searxng traefik` explicitly before
`up -d`, rather than attempting to pull the local GPU tag. If the installation
uses explicit `-f` options, include its GPU override in every command.

Let active agent tasks finish and back up persistent data before updating.
Restarting the server interrupts its tasks. `docker compose down -v` deletes
volumes and is not an update procedure.

## Host (default)

DOCKER_MODE=host preserves the existing behavior: services run on the host
Docker daemon, with Traefik subdomains and allocated host ports. Existing
installations do not need to change anything.

For services exposed by Traefik, use the same `http://<name>.localhost/` URL in
widgets, `browser_get` and `browser_act`. Prism's managed browsers resolve
`*.localhost` to the Docker DNS alias `traefik`, preserving the URL, Host header
and origin for assets, redirects, nested iframes and WebSockets. Bare `localhost`
and `127.0.0.1` still refer to the workspace itself. Scripts and custom tools can
continue using the internal container URL returned by the Docker tool.

In a user's browser, `*.localhost` points to the machine running that browser.
These links therefore work locally when Docker and the browser share a machine
and Traefik listens on port 80. A remote installation requires reachable
service URLs and matching reverse-proxy configuration; do not present
`*.localhost` as the remote server's address.

The proxy must share a Docker network with the workspace and the service. This
is already true in the default Compose installation. A custom deployment should
give its proxy the `traefik` network alias. Services launched by Prism set
`traefik.docker.network` to their workspace network so a shared proxy does not
choose another project's default network.

Compose-defined services need their usual Traefik labels and a network shared
with the proxy. Prism never connects networks automatically from an agent tool
call.

This browser routing applies only to the host Docker backend. Docker inside the
workspace and hosted/Cloud execution keep their existing service URLs and access
controls; no local proxy route is added there.

## Docker inside the workspace

Set these installation variables, then recreate prism-server with Compose:

    DOCKER_MODE=workspace
    WORKSPACE_DOCKER_SOCKET=unix:///run/user/1000/docker.sock
    WORKSPACE_DOCKER_USER=1000

This requires an **already provisioned** workspace containing a Docker CLI,
the Compose plugin, and a running daemon reachable through that local Unix
socket. The defaults target a rootless daemon owned by UID 1000; adjust the
UID and socket to the actual daemon. The standard Prism workspace images
do not currently provision this daemon. Merely changing the variable on an
ordinary installation will cause startup to fail with a configuration error.

Prism invokes the inner CLI as the selected user. The daemon must have access
to /workspace for shared service files. Its image/container storage must
persist separately from ephemeral runtime sockets. Networking must allow
published service ports to be reached on the workspace from prism-server.
Rootless networking and sandbox-specific port restrictions must be validated
by the operator; the option does not configure these automatically.

All service operations (run, list, logs, inspect, stop, exec and Compose) use
the internal socket. If it fails, Prism reports an error and never retries
these operations against the host daemon. Compose receives the validated
snapshot via stdin; its project directory is mapped into /workspace.

- Scripts in the workspace: http://127.0.0.1:<published-port>/.
- Browser links and widget iframes: use the URL returned by the Docker tool.
  The local default is /proxy/<published-port>/; a hosted installation can provide
  a private HTTPS origin per service, serving the application at /.
- Compose: publish the desired ports on 0.0.0.0 inside the workspace. Hosted
  service URLs are returned by Compose up/ps as well. These URLs are for HTTP
  services; publishing a database port does not make it an HTTP application.
- Host Traefik discovery and prism-svc-* host DNS do not apply.
- Published ports belong to the workspace, not to the VPS's public interfaces.
- Applications using absolute root paths may need a base-path setting for
  subpath proxying. This is not a promise of arbitrary application compatibility.

The configured socket is also passed as DOCKER_HOST to ordinary workspace
commands so scripts can use the inner Docker CLI. No host socket must be
mounted inside that workspace.

The inner daemon does not list installation services on the outer host, such as
the database or Proton Mail Bridge. An empty inner `docker ps`, an empty managed
service list, or no Compose file in `/workspace` does not prove that those
services are absent. Keep the scope of each check explicit. Account settings
can be saved without a live connection; verify the relevant app operation before
calling an integration working. Host-side setup may require the operator's host
terminal while the agent guides the steps.

## Security and Cloud status

This selects the Docker **service backend**, not an isolation technology.
The current local server still uses its operator Docker connection to execute
commands inside and manage the outer workspace. Switching modes does not
remove that authority from prism-server or make it a Cloud-safe control plane.

Prism does not add privileged mode, install a daemon, or weaken the host's
security settings. A sandboxed workspace, network restrictions, aggregate
CPU/RAM/PID/disk quotas (including image caches), and a separate authenticated
origin for Cloud applications remain operator responsibilities.

Hosted environments can provide a scoped execution and connection capability
instead of giving prism-server an outer Docker socket. The private host validates
the workspace assignment on each operation.

In a read-only workspace, install Python libraries with pip (the user site lives
in the persistent workspace). Put system dependencies in Docker images instead
of attempting apt installation into the read-only root filesystem.

With a private HTTPS service origin, open the URL while signed in to Prism.
Access belongs to the workspace owner and is revoked on logout. The service's
own login or API Bearer token is separate from the Prism login. Its browser
frontend can use root-relative API and WebSocket paths without a proxy prefix.
Embed the URL in an iframe. A widget fetching an API across origins still needs
normal CORS/credentials handling, or can use its existing /proxy/<port>/ route.
Starting a container does not publish an anonymous public website.
