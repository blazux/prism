// Runs on the content origin before the widget document is installed.
// The dashboard credential remains opaque here; the content gateway validates
// it and exposes only its widget routes.
(function () {
  const parameters = new URL(location.href).searchParams;
  const palette = {bg: '--bg1', fg: '--text', muted: '--text2', accent: '--accent', border: '--border'};
  function applyTheme(vars) {
    if (!vars || typeof vars !== 'object') return;
    for (const key of Object.values(palette)) {
      if (typeof vars[key] === 'string' && /^#[0-9a-f]{6}$/i.test(vars[key])) document.documentElement.style.setProperty(key, vars[key]);
    }
    if (vars['color-scheme'] === 'light' || vars['color-scheme'] === 'dark') document.documentElement.style.colorScheme = vars['color-scheme'];
  }
  function themeStamp() {
    const style = document.documentElement.style;
    return [...Object.values(palette).map(key => style.getPropertyValue(key)), style.colorScheme].join('|');
  }
  // Appearance alone travels in this URL, so the loader can paint before the
  // dashboard has requested a widget grant. Credentials still use postMessage.
  const initialTheme = {'color-scheme': parameters.get('scheme')};
  for (const [parameter, token] of Object.entries(palette)) initialTheme[token] = parameters.get(parameter);
  applyTheme(initialTheme);
  function showError() {
    const loader = document.querySelector('.widget-loader');
    if (loader) { loader.dataset.state = 'error'; loader.setAttribute('aria-busy', 'false'); }
    const status = document.getElementById('widget-status') || document.body;
    if (status) status.textContent = 'The widget could not be opened. Reload the page to try again.';
    if (ownerOrigin) parent.postMessage({type: 'prism-widget-load-error'}, ownerOrigin);
  }
  let ownerOrigin;
  try {
    const value = parameters.get('parent');
    const owner = new URL(value);
    if (!['http:', 'https:'].includes(owner.protocol) || owner.origin !== value || owner.origin === location.origin || parent === window) throw new Error('Invalid widget parent');
    ownerOrigin = owner.origin;
  } catch (_) {
    showError();
    return;
  }

  let rendering = false, latestTheme = null;
  addEventListener('message', async function (event) {
    const data = event.data;
    if (event.source !== parent || event.origin !== ownerOrigin) return;
    if (data?.type === 'prism-theme') {
      if (data.vars && typeof data.vars === 'object') {
        latestTheme = data.vars; applyTheme(data.vars);
        parent.postMessage({type: 'prism-widget-theme-ready', theme: themeStamp()}, ownerOrigin);
      }
      return;
    }
    if (data?.type === 'prism-widget-error') { showError(); return; }
    if (data?.type !== 'prism-widget-render' || rendering) return;
    if (typeof data.grant !== 'string' || typeof data.html !== 'string') return;
    rendering = true;
    const renderTheme = latestTheme;
    try {
      const response = await fetch('/__prism_widget/authorize', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        credentials: 'same-origin', cache: 'no-store', body: JSON.stringify({token: data.grant})
      });
      if (!response.ok) throw new Error('Widget access unavailable');

      // Copy only the legacy keys explicitly selected by the dashboard. Do not
      // overwrite newer values already saved on the content origin.
      const copied = {local: [], session: []};
      for (const kind of ['local', 'session']) {
        for (const [key, value] of Object.entries(data.storage?.[kind] || {})) {
          if (typeof value !== 'string') continue;
          try {
            const storage = kind === 'local' ? localStorage : sessionStorage;
            if (storage.getItem(key) === null) storage.setItem(key, value);
            copied[kind].push(key);
          } catch (_) {} // Keep the original value on the dashboard if storage is full.
        }
      }
      parent.postMessage({type: 'prism-widget-storage-migrated', keys: copied}, ownerOrigin);

      // document.open keeps this window's origin. The SDK uses the parent
      // origin to authenticate later credential refreshes without re-rendering.
      window.PRISM_PARENT_ORIGIN = ownerOrigin;
      document.open();
      document.write(data.html);
      document.close();
      // A theme chosen while access was loading is newer than the CSS already
      // composed in data.html. Apply it before painting the finished widget.
      if (latestTheme && latestTheme !== renderTheme) {
        for (const [key, value] of Object.entries(latestTheme)) {
          if (typeof value === 'string' && (key.startsWith('--') || key === 'color-scheme')) document.documentElement.style.setProperty(key, value);
        }
      }
    } catch (_) {
      rendering = false;
      showError();
    }
  });
  parent.postMessage({type: 'prism-widget-ready', theme: themeStamp()}, ownerOrigin);
})();
