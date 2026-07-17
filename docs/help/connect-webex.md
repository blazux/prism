# Connecting Webex

Talk to your group's **shared agent** from Cisco Webex. Unlike Slack/Telegram
(a single personal owner), Webex in Prism is **per group**: each group wires its
own bot, and that group's shared agent answers in the group's Webex spaces. Like
Slack it uses an outbound WebSocket, so **Prism needs no public URL**.

Setting up the bot is done by a **group admin** (or a global admin) in the
**Admin console → Shared agent** tab.

## 1. Create a Webex bot
1. Go to https://developer.webex.com/my-apps → **Create a New App → Bot**.
2. Give it a name and username (this is the name members will `@mention`), pick
   an icon, and create it.
3. Copy the bot's **access token** (shown once — save it now).

## 2. Configure the shared agent
In Prism: **Admin → Shared agent**, pick your **group**, and set the agent's
**name**, **model** and **system prompt**. This is the same shared agent used in
the in-app Room — the Webex bot *is* this agent.

## 3. Connect Webex
Still in **Admin → Shared agent**, in the **Webex integration** section:
1. Paste the bot **access token** into *Bot access token*.
2. Set the **per-sender permissions** (emails, comma/space/newline-separated;
   `*` = everyone in the space):
   - **Query knowledge base** — who may search the group's RAG.
   - **Modify knowledge base** — who may add to / delete from it.
   - **Trigger tools** — who may run other tools (web, MCP, etc.).
3. Click **Save Webex**. The state flips to **● Connected**.

## 4. Talk to the bot
1. In Webex, **add the bot to a space** (or start a 1:1 space with it).
2. **Group space** — the bot answers only when you **@mention** it (by its bot
   name). **1:1 space** — it answers every message.
3. Each space keeps its own conversation history. The bot ignores its own posts.

## Notes
- The bot runs with your group's rights: the global tool policy tightened by any
  group restrictions, plus the per-sender permission lists above. A blocked tool
  call is reported to the sender; the agent still answers without that tool.
- Multiple groups can each run their own bot at the same time — one bot token per
  group.
- To disconnect, open the same section and click **Disconnect** (clears the token
  for that group).
- Reaching the agent from Webex does **not** require Prism to be internet-facing.
