# MCP servers

An MCP (Model Context Protocol) server exposes tools over HTTP — GitHub,
Linear, a database, your own service. Connect one and its tools become part of
the agent's toolset immediately. MCP needs Postgres.

## Settings → MCP (personal mode)
- **Add an MCP server**: a **Name** (e.g. `github`), the server **URL**, and
  optionally a stored secret used as authentication, then **Connect**. The
  transport (Streamable HTTP or legacy SSE) is detected from the URL.
- **Authentication with a token**: the secret you pick is sent as a `Bearer`
  token. Create it in **Settings → Secrets** first — it then appears in the
  dropdown (*— No authentication —* otherwise).
- **OAuth servers** (Asana, Linear…): if the server asks for OAuth, a consent
  popup opens (*Authorize in the popup…*). Approve it; the popup closes itself
  and the status turns to **✓ Connected**. If nothing opens, allow popups for
  Prism and retry.
- On success the status shows how many tools were loaded, and the server
  appears above with its tool count and the tool cards.

Each server row has a toggle and a **✕**:
- **Toggle off** keeps the server configured but withdraws its tools from the
  agent (the cards dim). Toggle on to get them back.
- **✕** deletes the server after a confirmation.

Its tools also show in **Settings → Tools → MCP Tools**, where you can opt out
of individual ones.

## Asking the agent
The agent can list the connected servers and their tools ("which MCP servers
do I have?"). In personal mode it can also **add** one ("connect the MCP
server at http://host:3000 as github") and **remove** one. For a server that
needs a token, it first asks you for it through the secure secret dialog, then
uses that secret as the bearer token — an integration credential (email
password, bot tokens…) is refused for this purpose.

## Shared deployments
In multi-user mode MCP is **group-scoped**:

- **Personal servers are unavailable.** The add form is gone from Settings, and
  the agent's add/remove actions are refused with a pointer to the Admin
  console.
- A **group admin** connects servers in **Admin console → MCP**: pick the
  **Group**, enter **name**, **URL**, pick a group secret for auth (or create
  one inline from the picker), **Connect**. **remove** deletes one. OAuth works
  the same way as above.
- Members see the group's servers in **Settings → MCP** (*Managed by the group
  admin — available to your agent and the shared agent (read-only)*), and each
  tool in **Settings → Tools → Group MCP Tools**, where they can opt out for
  their own agent. Tools an admin locked show **🔒 admins only** or **🚫 disabled
  by admin** instead of a toggle.
- The shared agent and every member's agent call the same servers.
- Not in a group yet? Settings says *"You're not part of a group yet — ask an
  admin to add you to one to access shared MCP servers."*

## Troubleshooting
- **Connect fails**: check the URL is reachable *from the Prism server* (a
  container, not your laptop) and that the secret holds a valid token.
- **A tool disappeared**: the server may be toggled off, or an admin restricted
  the tool — see the tools help.
- Errors returned by the server are shown as-is under the form.

## Asking the agent

"List my MCP servers" always works. Adding or removing one through the agent works in single-user mode, and in shared mode for a **group admin** (the server is then added to the group, exactly as from the admin console); a plain member is told to ask a group admin. A server that requires an OAuth sign-in cannot be added by the agent — it needs the browser consent page from Settings → MCP or the admin console.
