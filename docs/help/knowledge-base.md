# Knowledge base (RAG)

The knowledge base lets the agent answer from *your* documents: manuals,
procedures, contracts, notes. Documents are split into passages, embedded, and
searched by meaning. It needs Postgres; while the embedder warms up, the tab
shows *RAG not ready: initializing…* and retries by itself.

**Settings → Knowledge** has two sub-tabs: **Documents** (this page) and
**Skills** (the reusable procedures the agent saves as it learns).

## Collections
A **collection** is a named folder of documents the agent searches as one unit.
Keep one topic per collection ("hr-policies", "product-manual") — the agent
searches a *specific* collection, not everything at once.

To create one, click **+** next to *Collections* (or **+ New collection**
above the upload zone):
1. **Name** — letters, numbers and hyphens; it is lowercased.
2. **Description — tells the agent when to search this collection**, e.g.
   *"Internal API documentation: endpoints, authentication, error codes."*

The description matters: it is what the agent reads to decide *when* to look
in that collection. A collection without one shows a warning (*No description
— the agent won't know when to search this collection*); edit it any time in the
dashed field under the collection's title.

## Adding documents
Select a collection, then drop files on the upload zone (or click it). The
**Add to** dropdown lets you switch the target collection. Supported: **PDF,
DOCX, TXT, CSV, MD, JSON**, up to **50 MB** each. PPTX decks are converted to
PDF first, so they work too. Progress is shown while a large file is parsed and
indexed (*Indexation 120/480 passages…*).

- **PDF** is parsed page by page: each passage remembers its page, and the
  agent cites page numbers in its answers.
- Everything else is indexed as text.

The document table lists **File, Size, Chunks, Updated**; **✕** removes a
document. **✕** next to a collection deletes it *and all its documents* after a
confirmation.

## Searching from chat
Just ask — "what does the manual say about the warranty?". The agent lists
the collections, picks the one whose description matches, searches it and
answers with the source file and page. If the answer seems to be missing, check
that the collection has a description and that the document was indexed
(non-zero **Chunks**).

## The agent can manage it too
- "Add `docs/manual.pdf` to a collection called product-manual" — the agent
  ingests workspace files itself (`rag_ingest`), creating the collection if
  needed. It handles **one file per call**; for a folder it lists the files and
  ingests them one by one.
- It can also index text it just produced (a summary, a research report), list
  collections and their documents, and delete a document or a collection.
- In **Notes**, the **Add to knowledge** button pushes a note into a
  collection.

## Shared deployments
In multi-user mode the knowledge base is **group-scoped and admin-curated**:

- Members see their group's collections in **Settings → Knowledge** read-only
  (*Group knowledge base — managed by your group admin*), and their agent
  searches them.
- A group admin manages it in **Admin console → RAG**: pick the **Group**,
  **+ New collection**, **Choose a file…** and **Upload** (uploading to a new
  name creates the collection), plus **description** and **delete** per
  collection. The shared agent and every member's agent search the same base.
- A member with no group has no knowledge base at all until an admin adds them
  to one.

## Asking the agent

The agent can do all of this itself: "index `reports/audit.pdf` into a collection called audits" creates the collection and ingests the file (one file per request — for a folder it lists the files and ingests them one by one), "describe the audits collection as …" sets the one-line description you see in this tab, and "delete the audits collection" removes it after confirming. In shared mode the same rule as the upload zone applies: a plain member's agent can search the group knowledge base but is refused when it tries to add, describe or delete — only a group admin's agent (or the admin console) changes it.
