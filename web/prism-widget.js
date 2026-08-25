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

  // prismChat(message) → send a message to the agent in THIS dashboard's live
  // chat (e.g. from an "Analyse" button): it lands in the chat input, opens the
  // panel, and sends — exactly as if the user typed it — so the agent's reply
  // streams into the panel where the user sees it. Routed via the parent
  // dashboard (postMessage), NOT a direct /api/chat POST — that runs a headless
  // turn whose answer never reaches the visible chat. Fire-and-forget.
  window.prismChat = function (message) {
    try {
      window.parent.postMessage({ type: 'sendChat', text: String(message) }, '*');
    } catch (_) {}
    return Promise.resolve();
  };
})();
