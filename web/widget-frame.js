// Runs on the content origin before the widget document is installed.
// The dashboard credential remains opaque here; the content gateway validates
// it and exposes only its widget routes.
(function () {
  let ownerOrigin;
  try {
    const value = new URL(location.href).searchParams.get('parent');
    const owner = new URL(value);
    if (!['http:', 'https:'].includes(owner.protocol) || owner.origin !== value || owner.origin === location.origin || parent === window) throw new Error('Invalid widget parent');
    ownerOrigin = owner.origin;
  } catch (_) {
    document.getElementById('widget-status').textContent = 'The widget could not be opened. Reload the page to try again.';
    return;
  }

  let rendering = false;
  addEventListener('message', async function (event) {
    const data = event.data;
    if (event.source !== parent || event.origin !== ownerOrigin || data?.type !== 'prism-widget-render' || rendering) return;
    if (typeof data.grant !== 'string' || typeof data.html !== 'string') return;
    rendering = true;
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
    } catch (_) {
      rendering = false;
      document.body.textContent = 'The widget could not be opened. Reload the page to try again.';
    }
  });
  parent.postMessage({type: 'prism-widget-ready'}, ownerOrigin);
})();
