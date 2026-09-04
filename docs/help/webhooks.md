# Webhooks

A webhook turns an inbound HTTP call into an agent turn. Whatever the caller
sends is wrapped in a prompt you write, and handed to the agent with its whole
toolset — so the sender needs to know nothing about Prism beyond one URL.

Set them up in **Settings → Webhooks**.

## What you configure

- **Prompt** — the instructions wrapped around the payload. Put `{{content}}`
  where the payload should land:

  ```
  Une alerte de supervision vient d'arriver :
  {{content}}

  Si elle est critique, préviens-moi sur Telegram. Sinon, ne fais rien.
  ```

  Leave the placeholder out and the payload is appended after your prompt. Leave
  the prompt empty and the payload is sent as-is.

- **Chat session** — optional. By default each webhook gets its own session
  (`webhook-<id>`), so an automated feed never lands in the middle of a
  conversation. Point several webhooks at the same session if you want them to
  share context.

- **Model** — optional, defaults to the deployment's model.

- **Also send the answer to…** — optionally push the agent's reply to Telegram,
  Slack or Webex on top of whatever the prompt made it do.

- **Wait for the agent** — off by default. See below.

## Calling it

The URL carries its own token:

```bash
curl -X POST "https://your-prism/api/webhook/<id>?token=<token>" \
     -H 'Content-Type: application/json' \
     -d '{"alert":"disk","host":"gx10","pct":97}'
```

The token can also travel as an `X-Prism-Token` header or a bearer token, for
senders that can set headers but not query strings. `GET` works too, with the
query string as the payload — handy for a device that can only fire a URL.

JSON is pretty-printed before it reaches the agent; models read an indented
object far more reliably than one long line.

## Synchronous or not

By default the call returns `202 Accepted` immediately and the agent works in the
background. This is almost always what you want: an agent turn routinely takes
longer than a sender is willing to wait, and most systems discard the response
body anyway. The agent has tools — let the prompt tell it where to put the
answer.

Tick **Wait for the agent** and the call blocks until the turn finishes and
returns the reply as JSON. Use it when the caller genuinely consumes the answer.

## Security

The token in the URL is the whole gate — the endpoint sits outside the
dashboard's login, because senders are machines with no Prism account. **Anyone
holding that URL can make your agent run**, with the tools your prompt leads it
to use. Treat it like a password: don't paste it in a public repo or a shared
dashboard, and delete the webhook if it leaks. Deleting it invalidates the URL
immediately.

The settings page shows the call count and the outcome of the last call, which
is the fastest way to tell whether a sender is actually reaching you.

## Asking the agent

"Create a webhook for my CI, summarize each event in one line, and send it to Telegram" creates it; the agent replies with the URL and the token to give the calling system (sent as an `X-Prism-Token` header, a Bearer token, or `?token=`). "List my webhooks" and "remove the CI webhook" work the same way. Webhooks created this way run in their own session, so a feed never lands in your chat.
