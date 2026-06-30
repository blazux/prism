# Connecting Google Calendar (bring your own OAuth app)

Google Calendar requires **OAuth 2.0** — it no longer accepts plain passwords.
Prism never ships a shared Google app (so your data stays between you and
Google); instead you create your **own** OAuth app once. It takes about 10
minutes. The agent can walk you through each step.

> Google reorganizes the Cloud Console regularly, and the OAuth setup now lives
> under **"Google Auth Platform"**. The steps below match that newer layout, but
> exact button labels may differ. If what the user sees doesn't match, adapt —
> and if needed, web-search "Google Cloud create OAuth client ID <current year>"
> for the current screens rather than insisting on these exact labels.

## Does Prism need to be reachable from the internet? No.
A common worry — but the OAuth redirect goes to **your browser**, not to Google's
servers. Google never connects to Prism. After you approve, Google redirects your
browser to Prism's callback URL, and Prism then calls Google's token endpoint
**outbound** (which already works). So no inbound/public access is required.

The only real constraint is which redirect URI Google accepts:
- **Simplest (fully local):** open Prism at `http://localhost:48080` and register
  `http://localhost:48080/api/oauth/google/callback`. Google allows `http://`
  for `localhost` / `127.0.0.1` (loopback).
- A **LAN IP over http** (e.g. `http://192.168.1.x:48080`) is often **rejected**
  by Google — use loopback or an https domain instead.
- A **domain over https** (e.g. via your reverse proxy) works, even if it only
  resolves on your LAN — Google only checks the format, never reachability.
- **Remote server?** Either use an https domain, or SSH port-forward
  (`ssh -L 48080:localhost:48080 your-server`) and use `http://localhost:48080`.

The Redirect URI shown in **Settings → Calendar → Google** already reflects the
address you reached Prism at — copy that exact value into your Google app.

## 1. Create a Google Cloud project
1. Go to https://console.cloud.google.com/ and sign in.
2. Top bar → project dropdown → **New Project**. Name it (e.g. "Prism") → Create.

## 2. Enable the API
1. In the project, open **APIs & Services → Library**.
2. Search for **Google Calendar API** and click **Enable**.

## 3. Configure the OAuth consent / Auth Platform
Open **APIs & Services → OAuth consent screen** (it now opens the **Google Auth
Platform**). If it's not set up yet, click **Get started** and complete the
short wizard:
1. **App Information**: App name + a User support email → Next.
2. **Audience**: choose **External** → Next.
3. **Contact Information**: your email → Next.
4. Agree to the policy → **Create**.

Then add yourself as a tester so the app works without Google verification:
- Open the **Audience** tab. Publishing status will say **Testing**. Under **Test
  users**, click **Add users**, add your own Google email, and Save.

(You normally don't need to pre-declare scopes for this — Prism requests the
calendar scope at sign-in. If there's a **Data Access** tab and you prefer to add
it, the scope is `https://www.googleapis.com/auth/calendar`.)

## 4. Create the OAuth client credentials
1. Go to **APIs & Services → Credentials** (or the **Clients** tab in Google Auth
   Platform) → **Create credentials → OAuth client ID** (or **Create client**).
2. Application type **Web application**.
3. Under **Authorized redirect URIs**, add your Prism URL plus
   `/api/oauth/google/callback` — for example
   `http://localhost:48080/api/oauth/google/callback` (use the exact address you
   open Prism at; copy the Redirect URI shown in Settings → Calendar → Google).
4. Create. Copy the **Client ID** and **Client secret**.

## 5. Connect in Prism
In **Settings → Calendar → Google**, paste the Client ID and Client secret and
click **Save credentials**, then **Authorize**. A Google consent window opens;
approve it, and Prism receives a token it refreshes automatically. The Calendar
app now shows your Google Calendar.

Note: this connects calendar **events** only. For Google tasks, use Todoist or
keep tasks in CalDAV / Prism.

## Troubleshooting
- "redirect_uri_mismatch": the redirect URI in Google must match exactly what
  Prism uses, including http/https, host and port.
- "access blocked / app not verified": add your account under **Test users**, or
  publish the app. Personal use in Testing mode is fine.
