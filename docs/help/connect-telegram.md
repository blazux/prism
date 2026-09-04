# Connecting Telegram

Chat with **your personal agent** from your phone: your workspaces, your
knowledge, your permissions. Prism polls Telegram over an outbound connection,
so it needs **no public URL**. You create a bot once and paste its token. The
agent can walk you through it.

## 1. Create a bot with BotFather
1. In Telegram, open a chat with **@BotFather** and send `/newbot`.
2. Give it a display name, then a username ending in `bot`.
3. BotFather replies with the bot **token** — a string like
   `123456:ABC-DEF…`. Copy it and keep it private: whoever holds it can talk to
   your agent as you.

## 2. Save the token in Prism
1. **Settings → Channels**, select the **Telegram** card.
2. Paste the token into **Bot token** and click **Save token**. The status
   reads *Token set — send /start to your bot to link this chat.*

## 3. Link your chat
1. Open your new bot in Telegram and send **/start**.
2. It answers *"✅ This chat is now linked to your Prism agent. Send me
   anything."* Back in Settings, the card shows **Linked** and the state line
   turns to *✓ Linked and active.*

**Only the first chat to send /start is allowed.** Anyone else who writes to
the bot gets *"⛔ This Prism agent is linked to another chat."* — the bot is
yours alone.

## 4. Talk to it
Send messages as you would in the dashboard chat. A typing indicator shows
while the agent works, and long answers are split into several messages. You
can also send a **document or a photo** (with an optional caption): it is
attached to the message and the agent can read it.

## Scheduled delivery
Once linked, ask in chat: *"every morning at 8, summarize my unread emails and
send it to me on Telegram"*. The agent schedules a cron job whose result is
pushed to your Telegram chat — no need to be at the dashboard.

## Unlinking and changing the bot
- **Unlink chat** (Settings → Channels) forgets the linked chat; the next
  `/start` re-links, so you can move to another phone or account.
- Pasting a **new token** replaces the bot and also forgets the linked chat —
  send `/start` again from the new bot.

## Notes
- In a shared deployment Telegram is **per user**: every member connects their
  own bot to their own agent, from their own Settings.
- The secure secret dialog is dashboard-only: if a task needs a new credential,
  the agent cannot ask for it over Telegram — add it in **Settings → Secrets**
  first.
- Manual tool approval does not apply over Telegram: tool calls run in auto
  mode.
- A single Telegram turn is limited to 10 minutes.
