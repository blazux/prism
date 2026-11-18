# Private Prism demo workspace

Build from the Prism repository root, using the CPU workspace:

```sh
docker build -f Dockerfile.agent -t blazux/prism-workspace:demo-cpu-base-20261112 .
docker build -t blazux/prism-workspace:demo-dind-20261112 deploy/demo/workspace
```

The prepared image uses CPU base
`sha256:ce5345c3febea6a53542baa7feaa53553a4873bb206113b4504abb9a18cb39d8`
and has image ID
`sha256:45e435bb3af2b55774e38e0fbfbc242b0a33cb7f192384aca4665c464af30a0d`.
Its uncompressed layers occupy about 3.78 GiB. Two workspaces using this image
share those image layers; their Docker data and working files remain separate.
Use the prepared image to reproduce the tested artifact; a rebuild can resolve
newer distribution or Python packages.

For each independent stack, use a separate workspace volume mounted at
`/workspace` and a separate Docker data volume mounted at `/var/lib/docker`.
The workspace service requires `privileged: true` and `cgroup: private`.
Do not mount the VPS Docker socket into this workspace.
The image provides a Docker readiness healthcheck; start Prism after the
workspace becomes healthy. Do not override the entrypoint or provide the normal
workspace's Compose startup command: this image already restores packages and
cron jobs before supervising its Docker daemon.

Prism server configuration:

```dotenv
DOCKER_MODE=workspace
WORKSPACE_DOCKER_SOCKET=unix:///var/run/docker.sock
WORKSPACE_DOCKER_USER=0
AGENT_CONTAINER=<this-stack-workspace-container-name>
```

The server still uses the operator Docker connection to execute commands inside
its outer workspace. Services started by its Docker tools use only the daemon
inside that workspace. Their URLs are the native `/proxy/<port>/` URLs.

The image copies the official Docker 29.9.3 binaries, Compose, Buildx and DinD
entrypoint. The daemon uses a Unix socket only, the classic overlay2 store and
bounded local log files. The entrypoint restores `.apt-packages`,
`.pip-packages` and `.crontab`, then keeps the workspace alive with its daemon.
Memory, CPU and PID limits on the outer workspace also bound its inner services.
The 768 MiB demo limit is suitable for small services; browser automation and
large Python workloads may require stopping other demo work or raising it.

This is a private operator-controlled demonstration deployment. Privileged DinD
is not a security boundary for untrusted public tenants.
