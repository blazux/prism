# Notes and wiki pages

Open **Notes** to write Markdown pages, find them by title, text or tags, and
follow links between them. Notes live in Prism's database or in the Markdown
vault selected in **Settings → Notes**. The selected source belongs to your
account, independently of the workspace open in the dashboard.

## Write with the assistant

With a note open, ask for a change in chat. The assistant uses `editor read`
to see your current text, including unsaved typing, then `editor update` with
that revision. You see the changes in the same editor. If you type or switch
notes in between, the assistant must read again before replacing anything.

For other saved pages, the existing `note` tool can list, create and edit them.
Copy IDs from a fresh list rather than guessing. Keep unrelated fields when
editing a page. A failed save leaves the text visible so it can be recovered.

## Versions and trash

Edits saved through Notes and the native note/editor tools keep previous
versions. Open a page's **History** to
preview an earlier title, body and tags, then restore the version you want.
Restoring also preserves the current saved version. Save current typing before
switching versions; if saving fails, keep the editor open and resolve the error.

**Move to trash** removes a page from the active list and keeps a recoverable
copy. Open **Trash** to restore it. The assistant's existing `note delete`
action uses the same recovery mechanism. Restoring a vault file refuses to
overwrite another file that has taken its original path.

For a Markdown vault, recovery data lives in the vault's hidden
`.prism-notes` directory. Keep that directory with the vault when backing it
up. Raw Markdown and frontmatter are preserved. Changes made directly to vault
files by another editor, a script or a file-writing tool are not automatically
recorded in this version history. Use the native note tools when recovery of
those edits is needed.

## Link pages

Use `[[Page title]]`, a vault-relative path such as `[[runbooks/network]]`,
or `[[Page title|label to display]]`. If several accessible pages match a
title, choose the intended page. A missing target can be created as a new note.
Links written inside code examples remain literal text.

Title and alias links resolve within the page's personal or group scope.
Use **Insert page link** for an explicit link to an accessible page in another
scope.

The link picker inserts links to specific pages. Database note IDs stay stable
when their title changes. Renaming through Prism also keeps old titles and
vault paths as aliases, so existing links can still find the page. An external
file rename is not automatically registered as a Prism alias.

**Backlinks** shows accessible pages linking to the current page. Use the page
outline to jump between headings, and back/forward navigation to revisit pages.
Link resolution and backlinks only use notes the current account can read.

## Publish a note

In shared mode, sharing publishes a copy to the group. Members can read it;
the author and group admins can edit it. Personal edits do not automatically
replace the published copy: share the note again to refresh it. History and
restoration of a published page follow its management permissions.

**Add to knowledge** indexes a copy in a document collection. Edit the note,
then update that indexed copy when the assistant should retrieve the new text.
Publishing to a group and indexing for document search are separate actions.

## Recovery from chat

Use `exec_command` and the existing `PRISM_URL` / `PRISM_TOKEN` environment
variables for recovery when no browser editor is open. Keep credentials in
the authorization header and never display them.

- `GET /api/notes/trash` lists the deleted notes you may restore.
- `GET /api/notes/history?id=ID&scope=personal` lists a page's version
  metadata. Follow `nextBefore` with `before` for older entries; add `version`
  with a returned version ID to read that snapshot's body. For a published
  group page use `scope=group`.
- `POST /api/notes/restore` accepts `id` and `scope` to restore from trash.
  Add `version` with an exact returned version ID to restore that snapshot.

Read the current page and version before restoring. Ask which version the user
wants when ambiguous. Use the returned note ID for subsequent operations;
vault restoration can involve a file path.
