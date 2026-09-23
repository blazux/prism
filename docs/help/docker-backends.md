# Docker backends

Prism's Docker tools keep the same names and parameters. The deployment chooses
where services run; use the URLs returned by the tools rather than guessing.

## Host (default)

DOCKER_MODE=host preserves the existing behavior: services run on the host
Docker daemon, with Traefik subdomains and allocated host ports. Existing
installations do not need to change anything.

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
