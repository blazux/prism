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
   widget/plugin ids and the `.secret_key` file are stable identifiers. A new
   version reads what the old one wrote. If a format must evolve, the new
   version migrates on read (as `migrateUserScopedConfig` does) and never
   requires the old data to be gone.

3. **Environment variables are stable.** Names in `.env.example` are never
   renamed or removed; a new variable is optional with a sensible default.
   `docker-compose.yml` keeps every existing service, volume and port.

4. **The agent's world is stable.** Tool names (including legacy aliases),
   the widget runtime API (`prismTool`, `prismChat`, `prismNotify`,
   `prismSuggest`, `prismContext`, `prismOpenFile`), the variables injected
   into cron/custom tools (`$PRISM_URL`, `$PRISM_SESSION`, `$PRISM_TOKEN`)
   and the routes the system prompt documents (`/api/builtin/<tool>`,
   `/api/tool/<name>`, `/api/notify`, `/api/chat`, `/api/secrets/<name>`,
   `/data/…`) keep their names and semantics. Widgets, custom tools, skills
   and crons written by the agent on the previous version must keep working
   unchanged.

5. **Encrypted data stays readable.** The secrets cipher (AES-256-GCM keyed
   by `.secret_key`) and its on-disk/in-DB format do not change.

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
