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

  // prismTool(name, args?) → the tool's result.
  // Works for ANY tool — a custom Python tool, a built-in, or an MCP tool — via
  // the one universal endpoint; you never choose an endpoint. JSON output is
  // parsed for you (so you get an array/object, not a string). A tool error
  // rejects with that error message, so wrap calls in try/catch.
  window.prismTool = async function (name, args) {
    const res = await fetch(
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
    const res = await fetch(
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

  // prismOnData(cb) → run cb() whenever the agent changes shared data (notes,
  // tasks, events) from elsewhere, so a widget refreshes on real change instead
  // of a blind setInterval. Returns an unsubscribe function.
  window.prismOnData = function (cb) {
    function h(e) { if (e && e.data && e.data.type === 'data-changed') cb(); }
    window.addEventListener('message', h);
    return function () { window.removeEventListener('message', h); };
  };
})();
