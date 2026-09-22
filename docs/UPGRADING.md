# Upgrading a running Prism

An existing instance upgrades by pulling the new image and restarting —
`docker compose pull && docker compose up -d` (or `build` if you build locally).
No manual step, no export/import, no downtime beyond the restart. This file is
the contract that keeps that true. Every change to `main` must respect it.

## What must never break

1. **Database schema is additive and idempotent.** `initSchema` runs at every
   start against whatever schema the previous version left. Only
   `CREATE … IF NOT EXISTS` and `ALTER TABLE … ADD COLUMN IF NOT EXISTS` are
   allowed; never `DROP`, `RENAME` or a type change. New columns carry a
   default or accept NULL. Pinned by `internal/memory/schema_compat_test.go`.

2. **Existing rows keep their meaning.** Config keys in `agent_config`, secret
   names, session-id formats (`u<id>-<board>`, `room-g<id>`, `webhook-<id>`),
   widget/plugin ids and the encryption key contents are stable identifiers. A new
   version reads what the old one wrote. If a format must evolve, the new
   version migrates on read (as `migrateUserScopedConfig` does) and never
   requires the old data to be gone.

3. **Environment variables are stable.** Names in `.env.example` are never
   renamed or removed; a new variable is optional with a sensible default.
   `docker-compose.yml` keeps every existing service, volume and port.

4. **The agent's world is stable.** `$PRISM_TOKEN` keeps its name and its
   `Bearer`/cookie usage; it is now a per-session capability token
   (`internal/server/captoken.go`) rather than the deployment token, but the
   deployment token itself is still accepted, so cron jobs and operator
   scripts that baked in the old value keep working. Capability tokens are a
   stateless HMAC of the deployment token, so a cron's baked token still
   verifies after a restart; rotating `PRISM_TOKEN` invalidates both, exactly
   as it already did for hand-written crons. Tool names (including legacy aliases),
   the widget runtime API (`prismTool`, `prismChat`, `prismNotify`,
   `prismSuggest`, `prismContext`, `prismOpenFile`), the variables injected
   into cron/custom tools (`$PRISM_URL`, `$PRISM_SESSION`, `$PRISM_TOKEN`)
   and the routes the system prompt documents (`/api/builtin/<tool>`,
   `/api/tool/<name>`, `/api/notify`, `/api/chat`, `/api/secrets/<name>`,
   `/data/…`) keep their names and semantics. Widgets, custom tools, skills
   and crons written by the agent on the previous version must keep working
   unchanged.

5. **Encrypted data stays readable.** The secrets cipher (AES-256-GCM keyed
   by the persisted private key) and its on-disk/in-DB format do not change.

## What a change may do

- Add tables, columns, indexes, config keys, env vars, tools, routes.
- Tighten who may call a route (a member losing access to an admin-only
  endpoint is a security fix, not a compatibility break) — as long as the
  agent's own self-calls with `$PRISM_TOKEN` keep working.
- Change defaults for *new* installs (e.g. the Dockerfile's default model),
  never silently for existing ones: an existing `.env` wins.

## Checklist before merging

- [ ] `go test ./...` passes (includes the schema idempotency test).
- [ ] No `.env.example` key renamed or removed.
- [ ] No tool, alias, injected variable or documented route renamed.
- [ ] If a stored format changed: old data is migrated on read, and the
      migration is idempotent.
- [ ] Start the new build against a database from the previous version at
      least once.

## Security update: private key storage

Use the updated Compose file as well as the new server image. It adds the
`server-private` volume at `/var/lib/prism`, mounted only in prism-server.
At startup, Prism copies the existing workspace `.secret_key` into
`/var/lib/prism/secret.key`, verifies that the keys match, then removes the
workspace copy. Encrypted database rows and credentials remain unchanged.
A conflicting destination key stops startup instead of replacing either key.

Back up the database **and the server-private volume**. The workspace alone
is no longer sufficient to restore credentials. Never regenerate a key to
resolve a decryption error. For non-Docker installations, `SECRET_KEY_PATH`
can select a persistent file outside the workspace; by default it is
`../.prism-private/<workspace-directory-name>.key` relative to the workspace.
Custom deployments must persist this private location across replacements.
An older server expects the old location: rollback requires restoring the
same key there while the server is stopped.

Generated files, screenshots, plugins and proxied applications now require
authentication even in single-user mode. Group capability tokens keep their
format, but are bound to their signed session and group permissions; unsigned
group identity and uid-zero tokens for unrelated multi-user sessions are
refused. Ordinary user sessions keep their tools and parameters.

Compose operations now fail closed on unreadable or invalid files and reject
host paths, indirect privileged volume definitions, includes, builds and
env_file. Use a published image, explicit environment values and ordinary
volumes. This local guard is not a sandbox for hostile Cloud tenants.

Login throttling uses the direct peer address (30 login attempts per minute,
10 signup attempts). Behind a reverse proxy those quotas are shared; distributed
Cloud throttling and trusted proxy configuration remain deployment work.
