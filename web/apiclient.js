/* One way to call the API from every app.
 *
 * Every app used to write `fetch(url).then(r => r.json())`, which skips the
 * status code by construction, and to ignore the result of DELETE/POST
 * entirely. A refusal then looked exactly like a success: the form closed, the
 * row came back on the next load, and nothing was ever shown. Worse, the
 * server answers errors two ways — writeErr sends {"error": "..."} as JSON,
 * http.Error sends plain text — so `.json()` sometimes parsed the error as if
 * it were data and sometimes threw into an unhandled promise.
 *
 * PrismAPI.api() throws on any non-2xx, carrying the server's own message
 * whichever shape it used, and PrismAPI.toast() puts it in front of the user.
 */
(function () {
  'use strict';

  async function failure(res) {
    const raw = (await res.text().catch(() => '')).trim();
    if (raw) {
      try {
        const d = JSON.parse(raw);
        if (d && typeof d.error === 'string' && d.error) return d.error;
        if (d && typeof d.detail === 'string' && d.detail) return d.detail;
      } catch (_) { /* plain text, use it as is */ }
      return raw.slice(0, 500);
    }
    return 'HTTP ' + res.status;
  }

  async function api(url, opts) {
    let res;
    try {
      res = await fetch(url, opts);
    } catch (e) {
      throw new Error('server unreachable: ' + ((e && e.message) || e));
    }
    if (!res.ok) throw new Error(await failure(res));
    const raw = await res.text();
    if (!raw) return {};
    if (!(res.headers.get('content-type') || '').includes('json')) return raw;
    try { return JSON.parse(raw); } catch (_) { return {}; }
  }

  const body = (method, data) => ({
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });

  let box = null;
  function toast(message, kind) {
    if (!box) {
      box = document.createElement('div');
      box.style.cssText =
        'position:fixed;left:50%;transform:translateX(-50%);bottom:18px;z-index:9999;max-width:min(560px,92vw);' +
        'padding:10px 14px;border-radius:8px;font-size:13px;line-height:1.35;cursor:pointer;' +
        'box-shadow:0 4px 18px rgba(0,0,0,.25);white-space:pre-wrap;word-break:break-word';
      box.addEventListener('click', () => { box.style.display = 'none'; });
      document.body.appendChild(box);
    }
    const bad = kind !== 'ok';
    box.style.background = bad ? 'var(--danger, #e05252)' : 'var(--accent, #3cb878)';
    box.style.color = '#fff';
    box.textContent = String(message || (bad ? 'Something went wrong.' : 'Done.'));
    box.style.display = 'block';
    clearTimeout(toast._t);
    toast._t = setTimeout(() => { if (box) box.style.display = 'none'; }, bad ? 9000 : 3000);
  }

  // guard(fn) runs fn and shows whatever it throws instead of letting the
  // rejection disappear into the console.
  function guard(fn) {
    return async function (...args) {
      try { return await fn.apply(this, args); } catch (e) { toast((e && e.message) || e); }
    };
  }

  window.PrismAPI = { api, body, failure, toast, guard };
})();
