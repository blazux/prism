# Workspace history (undo)

The agent's `/workspace` — where it writes files, scripts and data — is a
**git repository**. After every agent turn, whatever changed is recorded as one
commit, so a file that got clobbered or a change that went wrong can be
inspected and rolled back. This is a safety net: it runs in the background
after the reply is sent and never slows the agent down, and if git is
unavailable it is simply off.

## What gets recorded
- The repository is created automatically at the first change; the existing
  workspace becomes the *baseline* commit.
- Each turn's commit is titled after your message (*turn: make me a weekly
  report script*). A turn that changed nothing produces no commit.
- Every agent turn counts, whatever started it — dashboard chat, Telegram,
  Slack, Webex, a webhook or a cron job that asks the agent something. A cron
  job that only runs a script of its own is not an agent turn, so its file
  changes are picked up by the *next* turn's commit.

## Asking for the history
Say *"show me the workspace history"*. The agent lists the recent changes
(newest first, 20 by default, up to 50) as `hash  when  message`:

```
a1b2c3d  5 minutes ago  turn: fix the report script
9f8e7d6  2 hours ago    turn: create data/report.py
```

Before the first change it answers *"Workspace history isn't available yet —
versioning starts on the first change."*

## Restoring a file
Say *"restore `data/report.py` to how it was in a1b2c3d"* (or *"…before your
last change"* — the agent looks up the hash for you). Only that path is
touched; the restoration is itself committed (*restore data/report.py from
a1b2c3d*), so nothing is ever lost — the state you replaced stays in history
and can be brought back the same way. Restoring a file, not a whole snapshot,
is deliberate: other work done since is kept.

## What is excluded
Transient, regenerable or secret files never enter history — the repository
gets a `.gitignore` with:

- screenshots (`.screenshots/`), logs (`logs/`, `*.log`, `*.pid`), browser
  session state (`.browser_session_*`)
- caches (`data/cache/`, `*-cache.json`, `tmp_*`, `*.tmp`)
- generated Python files (`__pycache__/`, `*.pyc`)
- secrets and keys (`.env`, `.env.*`, `*.key`, `*.pem`, `*.p12`)

If you wrote your own `.gitignore` in the workspace, it is respected as-is.

## Notes
- Power users can open the Terminal (Ctrl+Enter) and use `git log` /
  `git diff` in `/workspace` directly — it is a normal repository.
- History is about *files in the workspace*. Notes, tasks, events, widgets on a
  board and chat history are stored elsewhere and are not covered by it.
