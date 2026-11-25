// prism-widget.js — injected into every Prism widget (dashboard iframe and the
// /plugins/ preview alike). It gives widget code one clean way to talk to Prism
// so the agent never has to hand-write fetch/endpoint/session/parse boilerplate:
//
//   const tickets = await prismTool('rt_dbs_tickets');           // any tool
//   const ticket  = await prismTool('get_ticket', { ticket_id }); // custom, built-in OR MCP
//   prismChat('Analyse ce ticket : ' + JSON.stringify(ticket));   // message the agent
//
// window.PRISM_SESSION is set by Prism on the line just above this script.
(function () {
  function session() {
    return window.PRISM_SESSION || '';
  }

  // Refresh the content-origin credential without replacing the running app.
  // The dashboard remains the only source of grants; widget code never receives
  // the dashboard's cookie or an unrestricted API credential.
  const ownerOrigin = window.PRISM_PARENT_ORIGIN;
  let authorization;
  if (ownerOrigin && window.parent !== window) window.addEventListener('message', async function (event) {
    if (event.source !== window.parent || event.origin !== ownerOrigin || event.data?.type !== 'prism-widget-authorize' || typeof event.data.grant !== 'string') return;
    try {
      const response = await fetch('/__prism_widget/authorize', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        credentials: 'same-origin', cache: 'no-store', body: JSON.stringify({token: event.data.grant})
      });
      if (!response.ok) throw new Error('Widget access unavailable');
      authorization?.resolve();
    } catch (error) { authorization?.reject(error); }
  });
  function refreshAuthorization() {
    if (authorization) return authorization.promise;
    let resolve, reject;
    const promise = new Promise(function (yes, no) { resolve = yes; reject = no; });
    const timer = setTimeout(function () { reject(new Error('Widget access unavailable. Reload the page to retry.')); }, 10000);
    authorization = {promise, resolve, reject};
    promise.finally(function () { clearTimeout(timer); authorization = null; }).catch(function () {});
    window.parent.postMessage({type: 'prism-widget-authorization-needed'}, ownerOrigin);
    return promise;
  }
  async function widgetFetch(url, options) {
    const response = await fetch(url, options);
    if (response.status !== 401 || !ownerOrigin || window.parent === window) return response;
    await refreshAuthorization();
    return fetch(url, options);
  }

  // prismTool(name, args?) → the tool's result.
  // Works for ANY tool — a custom Python tool, a built-in, or an MCP tool — via
  // the one universal endpoint; you never choose an endpoint. JSON output is
  // parsed for you (so you get an array/object, not a string). A tool error
  // rejects with that error message, so wrap calls in try/catch.
  window.prismTool = async function (name, args) {
    const res = await widgetFetch(
      '/api/builtin/' + encodeURIComponent(name) + '?session=' + encodeURIComponent(session()),
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(args || {}),
      }
    );
    let data;
    try {
      data = await res.json();
    } catch (_) {
      throw new Error('tool ' + name + ' failed: HTTP ' + res.status);
    }
    if (data && data.error) throw new Error(data.error);
    let r = data ? data.result : undefined;
    if (typeof r === 'string') {
      try {
        return JSON.parse(r);
      } catch (_) {
        return r; // plain-text tool output — hand it back as-is
      }
    }
    return r;
  };

  // prismChat(message) → resolves when the dashboard's server accepts the
  // message. The agent's reply streams in the dashboard; acceptance does not
  // mean that the agent has finished the task. Busy or unavailable chat rejects.
  let chatRequestSequence = 0;
  window.prismChat = function (message) {
    return new Promise(function (resolve, reject) {
      if (window.parent === window) { reject(new Error('Open this widget in a dashboard to message the agent.')); return; }
      const id = 'prism-chat-' + Date.now() + '-' + (++chatRequestSequence);
      let timer;
      function finish(error, result) {
        clearTimeout(timer);
        window.removeEventListener('message', onResponse);
        if (error) reject(new Error(error)); else resolve(result);
      }
      function onResponse(event) {
        const data = event.data;
        if (event.source !== window.parent || !data || data.type !== 'prism-chat-response' || data.id !== id) return;
        if (data.status === 'accepted') finish(null, { status: 'accepted' });
        else finish(data.error || 'The message was not accepted.');
      }
      window.addEventListener('message', onResponse);
      timer = setTimeout(function () {
        finish('Message acceptance could not be confirmed. Check the chat before sending again.');
      }, 30000);
      try { window.parent.postMessage({ type: 'sendChat', id: id, text: String(message) }, '*'); }
      catch (error) { finish(error.message || 'The message could not be sent.'); }
    });
  };

  // prismToolRaw(name, args?) → the full { result, images } a tool returns, for
  // the rare tool that produces images (screenshot / vision / browser). prismTool
  // gives you just the parsed result; use this when you also need the images.
  window.prismToolRaw = async function (name, args) {
    const res = await widgetFetch(
      '/api/builtin/' + encodeURIComponent(name) + '?session=' + encodeURIComponent(session()),
      { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(args || {}) }
    );
    let data;
    try { data = await res.json(); } catch (_) { throw new Error('tool ' + name + ' failed: HTTP ' + res.status); }
    if (data && data.error) throw new Error(data.error);
    let r = data ? data.result : undefined;
    if (typeof r === 'string') { try { r = JSON.parse(r); } catch (_) {} }
    return { result: r, images: (data && data.images) || [] };
  };

  // toParent posts a message to the dashboard — the affordance helpers below wrap
  // it so a widget never hand-writes window.parent.postMessage (wrong type string,
  // wrong field name, or a forgotten '*' target used to break silently).
  function toParent(msg) { try { window.parent.postMessage(msg, '*'); } catch (_) {} }

  // prismNotify(message, {title, level}) → a dashboard toast + bell entry.
  window.prismNotify = function (message, opts) {
    opts = opts || {};
    toParent({ type: 'notify', title: opts.title, message: String(message == null ? '' : message), level: opts.level || 'info' });
  };
  // prismSuggest(items) → suggestion chips under the chat input. Each item is
  // { label, prompt, send? } — send:true fires it immediately when clicked.
  window.prismSuggest = function (items) { toParent({ type: 'suggest', items: Array.isArray(items) ? items : [] }); };
  // prismContext(text) → attach text as context for the agent's next message.
  window.prismContext = function (text) { toParent({ type: 'context', text: String(text == null ? '' : text) }); };
  // prismOpenFile(path) → open a workspace file in the editor pane.
  window.prismOpenFile = function (path) { toParent({ type: 'openFile', path: String(path) }); };

  // Ordinary object links open in the dashboard without reloading the widget
  // or losing the user's chat draft. The dashboard validates the destination.
  document.addEventListener('click', function (event) {
    if (event.defaultPrevented || event.button > 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    const anchor = event.target.closest && event.target.closest('a[href]');
    if (!anchor || anchor.hasAttribute('download')) return;
    let url, base;
    // srcdoc widgets inherit their parent's origin, while location.origin is
    // "null" for about:srcdoc. baseURI resolves their ordinary relative links.
    try { base = new URL(document.baseURI); url = new URL(anchor.getAttribute('href'), base); } catch (_) { return; }
    if (url.origin !== base.origin || url.pathname !== '/') return;
    if (!['email','notes','tasks','calendar'].includes(url.searchParams.get('app')) && !url.searchParams.get('workspace')) return;
    event.preventDefault();
    toParent({type:'open-reference',href:url.pathname + url.search});
  });

  // Signals contain no records: re-read the authorized tool/file/API on change.
  // The dashboard shares its existing socket across every widget. Only a
  // standalone preview opens an EventSource, closed after the last unsubscribe.
  const dataListeners = new Set();
  let dataSource = null;
  function deliverData(change) {
    const topic = change.topic || change.app || '*';
    dataListeners.forEach(function (listener) {
      const options = listener.options;
      if (topic !== '*' && options.topic && options.topic !== topic) return;
      if (change.scope && options.scope && options.scope !== change.scope) return;
      if (topic !== '*' && options.group && Number(options.group) !== Number(change.group)) return;
      try { listener.callback(Object.assign({}, change, {topic: topic})); }
      catch (error) { setTimeout(function () { throw error; }, 0); }
    });
  }
  function onDataMessage(event) {
    if (event.source === window.parent && event.data && event.data.type === 'data-changed') deliverData(event.data);
  }
  window.prismOnData = function (callback, options) {
    if (typeof callback !== 'function') throw new TypeError('prismOnData needs a callback');
    const listener = {callback: callback, options: typeof options === 'string' ? {topic: options} : (options || {})};
    if (!dataListeners.size) window.addEventListener('message', onDataMessage);
    dataListeners.add(listener);
    if (window.parent === window && !dataSource && typeof EventSource === 'function') {
      dataSource = new EventSource('/api/data-events?session=' + encodeURIComponent(session()));
      dataSource.addEventListener('change', function (event) {
        try { deliverData(JSON.parse(event.data)); } catch (_) {}
      });
    }
    return function () {
      dataListeners.delete(listener);
      if (!dataListeners.size) {
        window.removeEventListener('message', onDataMessage);
        if (dataSource) { dataSource.close(); dataSource = null; }
      }
    };
  };

  // Call after a custom backend has saved successfully. Shared data opts into
  // its group scope; emitting a signal grants no access to that backend.
  window.prismDataChanged = async function (topic, options) {
    options = options || {};
    const response = await widgetFetch('/api/data-events?session=' + encodeURIComponent(session()), {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({topic: topic, scope: options.scope, group: options.group})
    });
    let data;
    try { data = await response.json(); } catch (_) { throw new Error('Data signal failed: HTTP ' + response.status); }
    if (!response.ok || data.error) throw new Error(data.error || 'Data signal failed: HTTP ' + response.status);
    return data;
  };
})();
