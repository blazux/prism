# Connecting Slack

Chat with your Prism agent from Slack using a **Socket Mode** app — an outbound
WebSocket, so Prism needs no public URL. You create the app once and paste two
tokens into Prism. The agent can walk you through it.

## 1. Create a Slack app
1. Go to https://api.slack.com/apps → **Create New App → From scratch**.
2. Name it (e.g. "Prism") and pick your workspace → Create.

## 2. Enable Socket Mode
1. **Settings → Socket Mode** → toggle **Enable Socket Mode** on.
2. It asks you to create an **App-Level Token** with the scope
   `connections:write`. Create it and copy the **`xapp-…`** token.

## 3. Add bot scopes and install
1. **OAuth & Permissions → Scopes → Bot Token Scopes**, add:
   `chat:write`, `im:history`, `im:read`, `im:write`.
2. Click **Install to Workspace** and approve.
3. Copy the **Bot User OAuth Token** (**`xoxb-…`**).

## 4. Subscribe to direct-message events
1. **Event Subscriptions** → toggle on (with Socket Mode there's no URL to enter).
2. Under **Subscribe to bot events**, add **`message.im`**. Save.
3. If prompted, **reinstall** the app.

## 5. Connect in Prism
In **Settings → Channels → Slack**, paste the **Bot token** (`xoxb-…`) and the
**App-level token** (`xapp-…`) and click **Connect**. Then **DM your bot** in
Slack — the first person to message it is linked as the owner.

## Notes
- Only the **first user** to DM the bot is allowed (the agent has powerful
  tools — keep the tokens private).
- Scheduled jobs can push to Slack too — ask e.g. "every morning at 8, summarize
  my unread emails and send it to me on Slack".
- Reaching the agent from Slack does **not** require Prism to be internet-facing.
