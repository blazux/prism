# Resources and workspace cleanup

Open **Settings → Resources** to see widgets, custom tools, data files, managed
cron jobs and Docker services in your execution environment. Filter by workspace
or **Old / unassigned workspaces**. Creation provenance and detected consumers
help review the list; an item with no detected consumer is not necessarily junk.
The page works without a configured AI or embedding provider.

## Removing a widget or workspace

Use a widget's Delete action or the workspace rail's Delete action. The dialog
previews which resources will be removed and which will be kept. You can select
additional retained resources for cleanup; Prism rechecks usage when applying it.

- A widget's declared dedicated tools, cron and services are automatically
  removed only when Prism tracked their creation and no retained consumer uses
  them. Data files always require an explicit selection.
- Other dashboards, custom-tool references, cron dependencies and published
  gallery snapshots protect shared resources. A widget reading the output of a
  tool/cron also protects that producer when those file references are visible.
- Locked widgets require unlocking before individual deletion. Explicit deletion
  of an entire workspace includes its locked widgets.
- Workspace deletion stops its agent and waits for its subagents to finish
  cancelling before deleting resources. Foreground commands and their attached
  subprocesses are stopped too, with forced termination if necessary. If Prism
  cannot confirm the stop, deletion fails without removing the dashboard;
  restore workspace execution and retry. Tracked cron jobs whose execution is
  bound to that workspace are also considered for removal; shared consumers
  still protect them. Standalone jobs are not deleted merely because they were
  created from the same workspace.
- A backend recreated under the same name is a different resource: old links
  cannot automatically delete the replacement. Relink it after checking.
- Deleting a service removes its container, not its volumes or persistent data.
  Compose stacks remain manageable with `docker_compose`; lifecycle tracking
  does not infer that an entire project or volume belongs to one widget.

Existing resources are inventoried without inventing ownership. Legacy or
uncertain resources stay in place unless explicitly selected. In multi-user
mode, other users' resources and unknown shared files/tools cannot be claimed
by linking them; standard tool policies apply to every cleanup operation.

## Linking a widget's backends

Select backend resources in Settings → Resources, choose the widget, then
click **Link**. This replaces that widget's dedicated-resource declarations.
Only declare resources created for that widget, not arbitrary reusable tools.
Other observed consumers still protect them even when declared dedicated.

The agent uses the same capability. For example, after registering
`weather_fetch`, scheduling `weather_refresh`, and creating a widget:

```json
{"action":"link","widget":"weather","ids":["tool:weather_fetch","cron:weather_refresh","file:data/weather.json"]}
```

Call `resources` with that JSON, or supply the same ID array as `resources`
directly on `widget` add/update. IDs use `tool:<name>`, `cron:<name>`,
`service:<name>` and `file:<workspace-relative-path>`. Reuse exact names returned
by the creation tools; `resources action=list` is useful for existing resources.
For the agent, list defaults to the current dashboard and its dependencies.
Optional `widget` narrows it to one widget; `all=true` includes the accessible
environment and legacy resources. Results are compact JSON pages: follow
`next_offset` by passing it as `offset` with the same filters. Counts include
references omitted from the samples. Settings keeps the full inventory.
Ordinary widget removal needs only `widget action=remove`: it already checks
usage and reports the cleanup, without an environment-wide inventory.
Prism handles provenance, versions, usage checks and cleanup. The agent maintains
no manifest. Omitting `resources` on widget update preserves the links;
`resources: []` clears them.

## Maintenance and old workspaces

In Settings → Resources, select items and click **Review cleanup**. Read the
plan, then confirm. Shared/protected selections are retained. Selecting a whole
unused chain (cron, tool and data) allows cleanup in producer-first order.
Items marked **Missing** indicate broken references, not extra files to delete.
Repair the references or remove the obsolete widget.

The agent can use `resources action=cleanup ids=[...]` to preview the same plan.
`dry_run` defaults to true; applying it requires `dry_run=false` and an explicit
user cleanup request. Adding `widget=<id>` includes that widget and its dedicated
backends. `widget action=remove` automatically uses the common cleanup logic.

Failures stop cleanup before subsequent dependent resources disappear. Successful
removals and retained resources are reported; refresh the inventory and retry the
remaining operation after fixing the error. An empty crontab is persisted, so
removed jobs do not return when the workspace container restarts. UI and agent
cron mutations are serialized within the application.

## What Prism cannot infer

Arbitrary shell code, dynamically constructed paths, runtime-only dependencies,
and clients outside Prism cannot be exhaustively analysed. Declare those
relationships on the widgets that depend on them. Shell-created resources have
unknown provenance unless subsequently tracked through the supported creation
tools; editing an existing legacy file does not adopt it. Removing a cron stops
future scheduling, not an arbitrary shell process it has already launched.
Foreground cancellation is scoped to that command's process group. Explicitly
detached processes (for example, a new session started with `setsid`) and Docker
services are not implicitly stopped with an unrelated agent task. Managed
services continue to follow their declared resource dependencies.

Cleanup is not an undo system or a backup. Review explicit data deletion carefully.
