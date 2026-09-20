# Calendar and tasks

The app and the agent use the same personal calendar and task source, across
workspaces. Choose the source in **Settings → Calendar → Active sources**.
Calendar supports local storage, CalDAV, Google and Microsoft; tasks support
local storage, CalDAV and Todoist.

## Tasks

Add a title, priority and optional due date. Use the pencil to edit those fields;
clear Due to remove a deadline. The round checkbox completes or reopens a task;
the trash button deletes it after confirmation. Search matches titles, and the
filters show Today, Overdue, Upcoming or High priority. Overdue means before
today; today's tasks are not overdue simply because their time is midnight.
Show completed includes completed tasks when the provider returns them;
Todoist's active-task list does not return completed history.

Scheduled jobs remain in the separate **Scheduled** tab, with pause, edit and
delete controls. Phone calls shown among tasks are read-only; ask the agent to
cancel a call rather than ticking it as a to-do.

Ask “move this deadline to Friday”, “rename this task”, “show overdue tasks” or
“mark this done”. The agent uses `task action=update id=...` with only the changed
fields. Omitted fields stay unchanged; `due=""` clears the deadline.
`task action=list query=invoice filter=overdue` narrows the list. The available
filters are all, today, overdue, upcoming and high. Existing add, done, reopen
and delete actions remain available.

## Calendar

Use Month, Week, Day or Agenda, then click a day to create an event or an event
to edit it. Set title, start/end dates and times, optional location and
 description. Check **All day** for a day or a date range. In the form the end
date is the last included day; Prism handles the provider's exclusive end date.
Events crossing midnight or spanning several days appear on each affected day.
Errors remain visible and a failed save does not discard your form.

Ask “move this meeting to 3pm”, “change its location”, “remove its description”,
“book next Monday as a day off”, or “delete this event”. The agent uses
`calendar action=update id=...` with only changed fields. Moving only `start`
keeps the duration. Empty description/location clears them. Invalid dates and
an end before start are refused.

For `calendar action=add all_day=true`, pass `start` as a date. Omit `end` for
one day; for multiple days `end` is the **exclusive** date (the day after the
last included day). Timed events can use ISO dates with a timezone offset;
otherwise dates are interpreted in Prism's deployment timezone.

Prism preserves provider recurrence data when editing a series. Editing or
completing an individual CalDAV occurrence remains unsupported and is refused
rather than changing the whole series by accident. Create/manage complex
recurrences, invitations and alarms in the connected calendar app for now.

## Work on the open form with the agent

Open a task's edit button, focus **Add a task**, or open a calendar event/new
event form. Ask for the changes in chat. The agent uses the shared `editor`
tool: `action=read`, then `action=update` with the returned `revision` and only
changed fields. Tasks accept `title`, `priority` and `due` (empty clears it).
Calendar accepts `title`, `start`, `end`, `all_day`, `location`, `description`.
Changing only `start` preserves duration. All-day `end` is exclusive; Prism
translates it to the inclusive last day displayed in the form. ISO times without
an offset use the `timezone` returned by the editor (the browser's timezone).
New tasks have a date-only due field; existing task forms also accept a time.

Changes appear in the form but are not persisted until the user presses **Save**
or **Add**. The agent must not claim an event/task is saved after an editor
update. The existing `task` and `calendar` tools still perform direct stored
changes when explicitly requested. If the user types or switches forms during
an edit, the stale revision is refused; read again before making another change.
