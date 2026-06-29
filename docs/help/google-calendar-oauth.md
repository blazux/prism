# Connecting Google Calendar (bring your own OAuth app)

Google Calendar speaks CalDAV, but requires **OAuth 2.0** — it no longer accepts
plain passwords. Prism never ships a shared Google app (so your data stays
between you and Google); instead you create your **own** OAuth app once. It takes
about 10 minutes. The agent can walk you through each step.

## 1. Create a Google Cloud project
1. Go to https://console.cloud.google.com/ and sign in.
2. Top bar → project dropdown → **New Project**. Name it (e.g. "Prism") → Create.

## 2. Enable the API
1. In the project, open **APIs & Services → Library**.
2. Search for **Google Calendar API** and click **Enable**.

## 3. Configure the consent screen
1. **APIs & Services → OAuth consent screen**.
2. User type **External** → Create.
3. Fill app name and your email where required. Save and continue.
4. On **Scopes**, you can leave defaults. Save and continue.
5. On **Test users**, add your own Google account. Save.
   (Staying in "Testing" is fine for personal use — no Google verification needed.)

## 4. Create the OAuth client credentials
1. **APIs & Services → Credentials → Create credentials → OAuth client ID**.
2. Application type **Web application**.
3. Under **Authorized redirect URIs**, add your Prism URL plus
   `/api/oauth/google/callback` — for example
   `http://localhost:48080/api/oauth/google/callback` (use the exact address you
   open Prism at).
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
