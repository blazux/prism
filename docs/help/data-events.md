# Refresh widgets and scripts when data changes

`prismOnData` subscribes to changes without asking the agent to run again.
Notes, tasks and calendar writes made through Prism notify the signed-in user's
open views, including writes from native tools and direct HTTP requests. Shared
note writes notify the group. An email or cron tool completed in the current
chat also refreshes its existing views.

Each signal contains a topic and scope, never note text, messages or other
records. Read the current data through your existing authorized tool or API.
For example, a task widget can load initially and reload after a change:

```js
async function refresh() {
  try {
    const tasks = await prismTool('task', {action: 'list'});
    render(tasks);
  } catch (error) {
    showError(error.message);
  }
}
const stop = prismOnData(refresh, 'tasks');
refresh();
// Call stop() when this view no longer needs updates.
```

The callback receives `{topic, scope, group?, reason}`. Native topics are
`notes`, `tasks` and `calendar`. Omit the topic to receive all accessible
changes. You can instead pass options such as
`{topic: 'todo.shared', scope: 'group', group: 3}`. The group ID must be an
actual group you can access. On connection or reconnection, a wildcard topic
`*` requests a fresh read even when a topic filter is set.

## Notify changes from a custom backend

After saving successfully, publish a signal with your own stable topic:

```js
await prismTool('todo_shared', {action: 'add', title: title});
await prismDataChanged('todo.shared');
```

Other widgets using `prismOnData(refresh, 'todo.shared')` can now reload the
list. This uses the caller's personal scope, or the installation scope in
mono-user mode. For a shared group backend in multi-user mode, publish with
`prismDataChanged('todo.shared', {scope: 'group', group: groupId})`, using its
actual authorized group ID. With several groups, pass that same group ID to
both the publisher and subscriber. Topics use 1–80 lowercase letters, digits,
dots, colons, underscores or hyphens, starting with a letter.

For updates made by a script, custom tool or scheduled command, publish from
that backend after the successful save. Its existing injected credentials
keep the signal in the caller's scope:

```python
import json, os, urllib.request

# Save the data first, using the backend's existing locking and access checks.
request = urllib.request.Request(
    os.environ['PRISM_URL'].rstrip('/') + '/api/data-events',
    data=json.dumps({'topic': 'todo.shared'}).encode(),
    headers={
        'Authorization': 'Bearer ' + os.environ['PRISM_TOKEN'],
        'Content-Type': 'application/json',
    },
    method='POST',
)
with urllib.request.urlopen(request, timeout=10) as response:
    response.read()
```

Omit `scope` for personal data. In mono-user mode this uses the installation's
data scope. A signed shared-agent token defaults to its own group. For personal
tokens, `scope: 'group'` selects their group; pass `group` explicitly when needed.
A signal grants no access to the underlying file, tool or service: that backend
still needs the appropriate permissions. Share distributes a widget's interface;
the backend supplies shared data and the signals tell copies to reload it.

File writes, shell commands, external provider changes and MCP mutations are
not automatically detected. Publish explicitly after those changes, or use the
backend's webhook or refresh mechanism.

## Subscribe from a script

`GET /api/data-events` provides a Server-Sent Events stream using the same
authentication. For an existing custom tool or scheduled script:

```sh
curl --fail --no-buffer "$PRISM_URL/api/data-events?topic=todo.shared" \
  -H "Authorization: Bearer $PRISM_TOKEN"
```

Each `event: change` includes JSON in its `data:` line. Optional query filters
are `topic`, `scope=personal|group` and, with `scope=group`, `group=<id>`.
Without a scope filter the stream includes only scopes that identity can read.
Subscriptions never select another user's personal data. Group access is
checked again during delivery; removing a membership stops its group signals.

A deterministic script can reload state and call an existing API when a change
arrives. Keep the process running with your usual service manager and reconnect
when the stream closes. No model call is made by a subscription. A scheduled
command can also perform its update and publish once without a running listener.

Signals are live refresh hints, not an event archive or a reliable job queue.
Reload on the initial `*` signal after every reconnect, tolerate repeated
notifications, and make actions idempotent. Missed signals are recovered by
reading the stored data. Closing a stream or calling `stop()` releases its
subscription. Dashboard widgets share Prism's existing connection; standalone
widget previews share one event stream within their page.
