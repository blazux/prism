// Live editor protocol shared by built-in apps. The browser owns the revision,
// so an old model response cannot overwrite typing or another document.
window.PrismEditor = {
  connect(options) {
    let previous = '', revision = '', lastContext = '';
    function snapshot() {
      const state = options.read();
      const serialized = JSON.stringify(state);
      if (serialized !== previous) { previous = serialized; revision = (crypto.randomUUID ? crypto.randomUUID() : Array.from(crypto.getRandomValues(new Uint32Array(4)), n => n.toString(16)).join('-')); }
      return state ? { ...state, revision } : null;
    }
    function publish() {
      const state = snapshot();
      const text = state ? options.context(state) + ' Use editor action=read to see the current text, then editor action=update with its revision to edit it in place.' : options.idleContext();
      if (text !== lastContext) { lastContext = text; parent.postMessage({ type:'context', text }, location.origin); }
    }
    function result(state, offset = 0) {
      if (!state) throw Error('No editor is open. Ask the user to open a note, email composer, task form or calendar form.');
      const chars = Array.from(state.body || '');
      offset = Number.isInteger(offset) && offset >= 0 ? offset : 0;
      if (offset > chars.length) throw Error('Offset exceeds the current body length. Read again from offset 0.');
      let size = Math.min(8000, chars.length - offset), out;
      do {
        const end = offset + size;
        out = { ...state, body:chars.slice(offset,end).join(''), offset, total_chars:chars.length, truncated:end < chars.length };
        if (out.truncated) out.next_offset = end;
        if (new TextEncoder().encode(JSON.stringify(out)).length <= 18000) break;
        size = Math.floor(size / 2);
      } while (size > 0);
      if (new TextEncoder().encode(JSON.stringify(out)).length > 18000) throw Error('Editor metadata is too large to read safely. Shorten its title or recipient fields.');
      return out;
    }
    async function handle(args) {
      const state = snapshot();
      if (args.action === 'read') return result(state, args.offset);
      if (args.action !== 'update') throw Error('Expected read or update.');
      if (!state || state.revision !== args.revision) throw Error('The editor changed. Read it again before updating; nothing was overwritten.');
      if (state.readonly) throw Error('This editor is currently read-only.');
      const patch = {};
      for (const [key,value] of Object.entries(args)) {
        if (['action','revision'].includes(key)) continue;
        if (!options.fields.includes(key) || typeof value !== (options.types?.[key] || 'string')) throw Error('Unsupported editor field: ' + key);
        patch[key] = value;
      }
      if (!Object.keys(patch).length) throw Error('Pass at least one field to update.');
      await options.update(patch);
      publish();
      const updated = snapshot();
      if (!updated || updated.id !== state.id || updated.app !== state.app) return {status:'updated',app:state.app,id:state.id,active_editor_changed:true};
      return { status:'updated', ...result(updated) };
    }
    addEventListener('message', async e => {
      const d = e.data;
      if (e.source !== parent || e.origin !== location.origin || d?.type !== 'editor-request') return;
      let response;
      try {
        if (!Number.isFinite(d.expires_at) || Date.now() >= d.expires_at) throw Error('Editor request expired. Read again.');
        response = await handle(d.args || {});
      } catch (err) { response = {error:err.message}; }
      parent.postMessage({type:'editor-response',id:d.id,result:response},location.origin);
    });
    document.addEventListener('input', publish);
    document.addEventListener('change', publish);
    return {publish, snapshot};
  }
};
