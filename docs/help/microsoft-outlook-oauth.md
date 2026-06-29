# Connecting Microsoft / Outlook (bring your own OAuth app)

Outlook.com and Microsoft 365 calendars use the **Microsoft Graph API** with
**OAuth 2.0**. As with Google, you register your **own** app in Azure once, so
your data stays between you and Microsoft. The agent can guide you through it.

## 1. Register an app
1. Go to https://entra.microsoft.com/ (or the Azure portal) →
   **App registrations → New registration**.
2. Name it (e.g. "Prism").
3. Supported account types: pick **Personal Microsoft accounts** (for
   Outlook.com) or your org, as appropriate.
4. **Redirect URI**: platform **Web**, value = your Prism URL plus
   `/api/oauth/microsoft/callback`, e.g.
   `http://localhost:48080/api/oauth/microsoft/callback`.
5. Register, then copy the **Application (client) ID** (and **Directory (tenant)
   ID** if shown).

## 2. Add API permissions
1. **API permissions → Add a permission → Microsoft Graph → Delegated
   permissions**.
2. Add **Calendars.ReadWrite** and **Tasks.ReadWrite**.
3. Click **Grant admin consent** if you're on an org tenant (personal accounts
   consent at sign-in).

## 3. Create a client secret
1. **Certificates & secrets → New client secret** → add → copy the **Value**
   immediately (it's only shown once).

## 4. Connect in Prism
In **Settings → Calendar → Microsoft**, paste the Client ID, the Client secret
value, and (if asked) the Tenant ID. Click **Authorize**, approve the Microsoft
consent screen, and Prism stores a refreshable token.

## Troubleshooting
- "redirect_uri_mismatch": the redirect URI must match Prism's exactly.
- Secret not working: make sure you copied the secret **Value**, not its ID.
