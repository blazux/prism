// In-memory account preferences for browser fixtures; never use browser storage.
const nativeKeys = new Set([
  'prism-theme', 'prism-custom-themes', 'prism-ui-scale', 'prism-density',
  'prism-bgfx', 'prism-ui-font', 'prism-chat-side', 'prism-chat-default',
  'prism-window-snap', 'active-workspace', 'prism-workspace-icons',
  'chat-drawer-width', 'prism-cal-mode', 'prism-pinned-notes',
  'agent-config', 'prism-approval', 'prism-admin-group', 'vox-supervision-session',
])

function createPreferencesBackend({origin = 'http://fixture.invalid', owner = 'fixture-owner', values = {}, legacyImportAllowed = false} = {}) {
  const stored = {...values}
  return {
    handle(route) {
      const request = route.request(), url = new URL(request.url())
      if (url.origin !== origin || url.pathname !== '/api/preferences') return null
      const reply = (status, data) => route.fulfill({status, contentType: 'application/json', body: JSON.stringify(data)})
      const method = request.method()
      if (method !== 'GET') {
        if (!['PATCH', 'POST'].includes(method)) return reply(405, {error: 'Method not allowed'})
        let body
        try { body = request.postDataJSON() } catch (_) { return reply(400, {error: 'Invalid JSON'}) }
        if (body?.owner !== owner) return reply(409, {error: 'Preferences owner changed'})
        if (!body.values || typeof body.values !== 'object' || Array.isArray(body.values)) return reply(400, {error: 'Invalid preferences'})
        for (const [key, value] of Object.entries(body.values)) {
          if (!nativeKeys.has(key) || typeof value !== 'string') return reply(400, {error: 'Invalid preference'})
        }
        for (const [key, value] of Object.entries(body.values)) {
          if (method === 'PATCH' || !Object.hasOwn(stored, key)) stored[key] = value
        }
      }
      return reply(200, {owner, values: {...stored}, legacyImportAllowed})
    },
  }
}

module.exports = {createPreferencesBackend}
