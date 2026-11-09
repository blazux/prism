# Connecting Microsoft Teams

In shared mode, each Prism group can connect its own Microsoft Teams bot. The
bot uses that group's shared agent: the model, system prompt, turn limits,
knowledge base, MCP servers and tool restrictions configured in **Admin → Shared
agent**. Teams conversations have their own histories, separate from the Room
and Webex. Conversations and channel threads do not share a chat history.

The model runs on the backend configured in Prism. No Copilot subscription or
Microsoft-hosted language model is required by this connector.

## Before you start

You need a Microsoft work or school tenant where you can register an application
and install a custom Teams app. Your Microsoft administrator may need to enable
custom app uploads. Configure one **single-tenant** bot per Prism group; a bot
application ID cannot be assigned to two groups. The connector supports the
Microsoft public cloud, not the separate sovereign-cloud environments.

Microsoft delivers messages to an HTTPS endpoint on Prism. Unlike the Webex
connection, Teams needs a **public HTTPS URL**, either directly or through a
reverse proxy. You can expose just `/api/teams/messages/<group-id>` to Microsoft
while keeping the dashboard behind your own network controls. The callback
authenticates Microsoft's signed Connector tokens; do not put a Prism login,
Basic authentication or an interactive access gateway in front of that path.

## 1. Register the bot

1. Create a single-tenant application in Microsoft Entra ID. Copy its
   **Application (client) ID** and **Directory (tenant) ID**.
2. Under **Certificates & secrets**, create a client secret. Copy its **Value**,
   not its Secret ID. Keep it out of chat messages and source control.
3. Create an **Azure Bot** resource linked to that application, using its existing
   application ID, tenant ID and secret. Select the single-tenant app type.
4. Enable the **Microsoft Teams** channel on the bot resource.

Microsoft's setup guide is available at
https://microsoft.github.io/teams-sdk/teams/azure-configuration/.
The bot registration does not require hosting Prism on Azure.

## 2. Connect the Prism group

As a group admin or global admin, open **Admin → Shared agent**, select your
group and find **Microsoft Teams integration**.

Enter the application ID, tenant ID and client secret value. Set the permissions
below, then click **Save Teams**. The secret is encrypted in Prism's secret store
and is never returned by the configuration API. Leave the secret field blank on
later saves to keep it. Changing the application or tenant requires a new secret.

Copy the **Messaging endpoint** into the bot resource's configuration in
Microsoft. It has this form:

```text
https://prism.example.com/api/teams/messages/3
```

Use your real public HTTPS hostname and the group number displayed by Prism.
If you open Prism at a local HTTP address, replace the origin with your public
HTTPS origin; keep the displayed path. Forward POST requests and the
`Authorization` header to Prism. Prism acknowledges messages before the agent
starts working, so your proxy does not need to keep the callback open for a
complete model response.

**Saved** means Prism has stored the configuration. It does not confirm that the
secret is valid, the Teams channel is enabled or Microsoft can reach the URL.

## 3. Choose who can use tools

The three permission fields accept **Entra user object IDs**, separated by spaces,
commas or newlines. Find a person's Object ID under **Microsoft Entra ID → Users
→ user → Overview**. These are user IDs, not email addresses, application IDs or
the `29:…` Teams-specific sender IDs.

- **Query knowledge base** allows knowledge searches.
- **Modify knowledge base** allows knowledge additions and deletions.
- **Trigger tools** allows the other tools, subject to the group's tool policy.

`*` allows everyone who can address this bot in the configured Microsoft tenant.
A blank field denies that category. For example, use `*` for knowledge searches
and list only the administrators' user object IDs for modifications and tools.
These lists do not create Prism accounts or turn Microsoft users into Prism
administrators. Group restrictions continue to apply even to an allowed sender.

## 4. Install the app and talk to it

Click **Download Teams app** after saving. In Teams, open **Apps → Manage your
apps → Upload an app**, upload the ZIP and install it for yourself, a team or a
group chat. The exact upload menu depends on your Microsoft administration
policy and Teams client.

In a direct chat, send a message normally. In a group chat or channel, **@mention
the bot**. It replies in that conversation or thread. By default, Teams does not
send it the surrounding messages that do not mention it. This connector does not
request permission to read all conversation messages.

Messages in the same conversation run in order. Different conversations can be
processed independently. Incoming retries are deduplicated in memory for up to
24 hours; this receipt cache resets when Prism restarts. Do not resend a
side-effecting request while its answer is still being prepared. Saturated inboxes
return HTTP 429 rather than acknowledging a message that cannot be queued.

## Images and files

Incoming supported inline images are saved in the workspace and passed through
Prism's usual vision handling. Personal-chat document attachments supplied with
a Teams download URL can also be imported. Downloads are limited to four files
per message and 10 MB per file. If an import fails, the agent receives an explicit
notice rather than being told that it has the file.

Stored documents in group chats and channels require Microsoft Graph access and
are not imported by this connector. **Uploading files in bot responses is not
implemented**: `add_attachment` is unavailable in Teams, so the agent must include
the result in its message. The connector does not turn Prism download URLs into
public file links.

## Data and history

Messages and imported attachments pass through Microsoft Teams. Prism saves chat
history in its database and imported files in its execution workspace, then sends
the conversation to its configured model provider. Using a local model does not
remove Microsoft from the Teams transport. The execution workspace follows
Prism's trusted-team deployment model; see [Shared mode](shared-mode.md).

## Scheduled answers

Mention the bot in the group chat or channel thread where you want announcements
to appear. In Prism, refresh **Announcement conversation**, select that
conversation and save. Private chats are excluded. The list contains the latest
100 group conversations or threads used with this bot.

An agent task can request delivery through the existing chat API with
`deliver: "teams"` and its own `$PRISM_SESSION`. Keep the group-scoped session:
Prism uses it to select the correct group's bot and announcement destination.
A personal session cannot deliver to a group's Teams conversation. A Room,
Webex and Teams session belonging to the same group can use that group's
announcement target.

## Disconnecting and troubleshooting

**Disconnect** clears the stored client secret and cancels queued and running
Teams turns for the group. Conversation history remains stored in Prism.
Configuration and previously discovered announcement conversations remain for
reconnection; changing the bot identity requires discovering new conversations.

If messages get no response, verify the public callback URL, application and
tenant IDs, client secret expiry, Microsoft Teams channel activation and app
installation. In group chats and channels, include an explicit @mention.
**Admin → Logs**, filtered on `teams`, shows agent and delivery failures without
printing the client secret. HTTP 401 indicates invalid Microsoft Connector
authentication; HTTP 403 indicates an activity whose tenant or identity does not
match the configured group. Emulator and unsigned test requests are refused.

The connector has not established access to a tenant merely because it appears
configured: a real message exchange in your Microsoft environment is the final
connection test.
