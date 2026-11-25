// Account preferences live on the server. This page keeps only a working copy.
// Old browser values are read solely for the one-way, non-overwriting import.
(function () {
  'use strict';
  const keys = new Set([
    'prism-theme', 'prism-custom-themes', 'prism-ui-scale', 'prism-density',
    'prism-bgfx', 'prism-ui-font', 'prism-chat-side', 'prism-chat-default',
    'prism-window-snap', 'active-workspace', 'prism-workspace-icons',
    'chat-drawer-width', 'prism-cal-mode', 'prism-pinned-notes',
    'agent-config', 'prism-approval', 'prism-admin-group', 'vox-supervision-session',
  ]);
  const structured = new Set(['prism-custom-themes', 'prism-workspace-icons', 'prism-pinned-notes', 'agent-config']);
  let values = {}, owner = null, autoImport = false, stopped = false, loaded = false, revision = 0, migrationBlocked = false;
  let timer, saving = null, loading = null, problem = '', initial = true;
  const pending = new Map();
  let channel;
  try { channel = new BroadcastChannel('prism-preferences'); } catch (_) {}

  function notify(changed, remote = false) {
    window.dispatchEvent(new CustomEvent('prism-preferences-changed', { detail: { keys: changed, remote, initial } }));
  }
  function status(message = '') {
    problem = message;
    if (!document.body) return;
    let box = document.getElementById('prism-preferences-status');
    if (!message) { box?.remove(); return; }
    if (!box) {
      box = document.createElement('div'); box.id = 'prism-preferences-status'; box.setAttribute('role', 'status');
      box.style.cssText = 'position:fixed;bottom:12px;left:12px;z-index:100000;max-width:min(460px,90vw);padding:12px;display:flex;align-items:center;gap:12px;background:var(--bg2,#202a3a);color:var(--text,#eef2fb);border:1px solid var(--border2,#4c6484);border-radius:8px;font:13px system-ui;box-shadow:0 4px 20px #0003';
      box.append(document.createElement('span'), document.createElement('button'));
      box.lastChild.type = 'button';
      box.lastChild.onclick = () => stopped ? location.reload() : refresh().then(flush).catch(() => {});
      document.body.append(box);
    }
    box.firstChild.textContent = message;
    box.lastChild.textContent = stopped ? 'Reload' : 'Retry';
  }
  function legacy() {
    const found = {};
    try {
      for (const key of keys) {
        if (Object.hasOwn(values, key)) continue;
        let value = localStorage.getItem(key);
        if (key === 'active-workspace' && value == null) value = localStorage.getItem('active-session');
        if (key === 'prism-admin-group' && value == null && owner) value = localStorage.getItem('prism-admin-group-' + (autoImport ? 'owner' : owner.replace(/^u/, '')));
        if (typeof value !== 'string' || value.length > 128 * 1024) continue;
        if (structured.has(key)) {
          try {
            const parsed = JSON.parse(value);
            if (key === 'agent-config') {
              if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) continue;
              const config = {};
              for (const field of ['disabled', 'combinedToolNotices']) if (Array.isArray(parsed[field])) config[field] = parsed[field].filter(name => typeof name === 'string' && name.length > 0 && name.length <= 256);
              value = JSON.stringify(config);
            }
          } catch (_) { continue; }
        }
        found[key] = value;
      }
    } catch (_) {} // A browser may block all access to its old store.
    return found;
  }
  function legacyManualApproval() {
    try { return localStorage.getItem('prism-approval') === 'manual'; } catch (_) { return false; }
  }
  async function request(method = 'GET', body) {
    const serialized = body ? JSON.stringify(body) : null;
    const response = await fetch('/api/preferences', {
      method, credentials: 'same-origin', cache: 'no-store',
      ...(body ? { headers: { 'Content-Type': 'application/json' }, body: serialized, keepalive: new TextEncoder().encode(serialized).byteLength < 60000 } : {}),
      signal: AbortSignal.timeout(15000),
    });
    if (response.status === 409 || (owner && (response.status === 401 || response.status === 403))) {
      stopped = true;
      status('Your account changed. Reload this page before saving preferences.');
    }
    if (!response.ok) {
      const error = Error('Preferences request failed');
      error.anonymous = response.status === 401 && owner === null;
      throw error;
    }
    const result = await response.json();
    if (!result || !result.values || typeof result.owner !== 'string') throw Error('Invalid preferences response');
    if (owner !== null && owner !== result.owner) {
      stopped = true;
      status('Your account changed. Reload this page before saving preferences.');
      throw Error('Preferences owner changed');
    }
    return result;
  }
  function accept(result, remote) {
    owner = result.owner; autoImport = result.legacyImportAllowed === true;
    loaded = true;
    const next = Object.fromEntries(Object.entries(result.values).filter(([k, v]) => keys.has(k) && typeof v === 'string'));
    for (const [key, value] of pending) next[key] = value;
    const changed = [...keys].filter(key => values[key] !== next[key]);
    values = next;
    if (changed.length || initial) notify(changed, remote);
    if (!pending.size && !stopped) status();
  }
  async function refresh() {
    if (stopped) return;
    if (loading) return loading;
    loading = (async () => {
      let result, started;
      // A GET started before a successful save must not restore its old value.
      do { started = revision; result = await request(); } while (started !== revision);
      owner = owner ?? result.owner;
      // Import only keys missing on the server. A human account requires an
      // explicit import: unscoped legacy values cannot identify their owner.
      let importProblem = false;
      if (result.legacyImportAllowed && initial) {
        const previous = values; values = result.values; autoImport = true;
        const old = legacy(); values = previous;
        migrationBlocked = Object.hasOwn(old, 'agent-config') || old['prism-approval'] === 'manual';
        // Preserve tool opt-outs and manual approval even if an unrelated old
        // palette is malformed or the remaining import exceeds the size limit.
        const toolSettings = {}, otherSettings = {};
        for (const [key, value] of Object.entries(old)) (key === 'agent-config' || key === 'prism-approval' ? toolSettings : otherSettings)[key] = value;
        for (const batch of [toolSettings, otherSettings]) {
          if (!Object.keys(batch).length) continue;
          revision++;
          try {
            result = await request('POST', { owner, values: batch });
            if (batch === toolSettings) migrationBlocked = false;
          } catch (_) { importProblem = true; break; }
          finally { revision++; }
        }
      }
      accept(result, true);
      if (importProblem && !stopped) status('Could not import your old preferences. They are still available in this browser.');
      initial = importProblem;
    })().catch(error => {
      if (!stopped && !error.anonymous) status('Could not load your preferences. Your unsaved changes are kept on this page.');
      throw error;
    }).finally(() => { loading = null; });
    return loading;
  }
  async function flush() {
    clearTimeout(timer);
    if (stopped) return false;
    if (saving) { const ok = await saving; return ok && pending.size ? flush() : ok; }
    if (!pending.size) return true;
    if (owner === null) { try { await refresh(); } catch (_) { return false; } }
    const sent = Object.fromEntries(pending);
    revision++;
    saving = (async () => {
      const result = await request('PATCH', { owner, values: sent });
      for (const [key, value] of Object.entries(sent)) if (pending.get(key) === value) pending.delete(key);
      // Do not reset unrelated controls using an older response while newer
      // local edits are queued. The next focus/remote invalidation reloads them.
      if (!pending.size) status();
      channel?.postMessage({ type: 'changed', owner });
      return true;
    })().catch(() => {
      if (!stopped) status('Could not save your preferences. Keep this page open and retry.');
      return false;
    }).finally(() => { saving = null; revision++; });
    const ok = await saving;
    if (ok && pending.size) return flush();
    return ok;
  }
  function setItem(key, value) {
    if (!keys.has(key)) throw Error('Unknown preference');
    if (structured.has(key) && !loaded) {
      status('Load your saved preferences before changing them. Keep this page open and retry.');
      return false;
    }
    value = String(value);
    if (values[key] === value || stopped) return;
    values[key] = value; pending.set(key, value);
    notify([key]);
    clearTimeout(timer); timer = setTimeout(() => { flush(); }, 180);
  }
  async function importLegacy() {
    await ready;
    if (stopped || owner === null) throw Error('Preferences unavailable');
    revision++;
    try {
      const result = await request('POST', { owner, values: legacy() });
      accept(result, true);
      channel?.postMessage({ type: 'changed', owner });
    } finally { revision++; }
  }
  const ready = refresh().catch(() => {});
  window.PrismPreferences = {
    ready, getItem: key => values[key] ?? null, setItem, flush, refresh,
    isReady: () => loaded && !stopped && !migrationBlocked,
    ensureReady: async () => { if ((!loaded || migrationBlocked) && !stopped) { try { await refresh(); } catch (_) {} } return loaded && !stopped && !migrationBlocked; },
    importLegacy, legacyCount: () => Object.keys(legacy()).length,
    needsLegacyImport: () => owner !== null && !autoImport && Object.keys(legacy()).length > 0,
    legacyManualApproval,
  };
  if (channel) channel.onmessage = event => {
    if (event.data?.type === 'changed' && event.data.owner === owner) refresh().catch(() => {});
  };
  const resume = () => { refresh().then(flush).catch(() => {}); };
  window.addEventListener('focus', resume);
  window.addEventListener('online', resume);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) resume(); else flush(); });
  window.addEventListener('pagehide', () => { flush(); });
  window.addEventListener('beforeunload', event => { if (pending.size) { flush(); event.preventDefault(); event.returnValue = ''; } });
  document.addEventListener('DOMContentLoaded', () => { if (problem) status(problem); });
})();
