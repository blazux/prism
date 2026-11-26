// Batch mailbox operations use the existing mail actions. Every request keeps
// the account, source folder and UID selected before the operation started.
async function runMailSelectionBatch(snapshot, perform, progress = () => {}, stopped = () => false) {
  const seen = new Set();
  const items = snapshot.items.filter(item => {
    if (!Number.isInteger(item.uid) || item.uid < 1 || item.uid > 0xffffffff || seen.has(item.uid)) return false;
    seen.add(item.uid); return true;
  }).map(item => ({...item}));
  const source = {...snapshot, items};
  const result = {total: items.length, confirmed: [], unconfirmed: [], unprocessed: []};
  let next = 0;
  async function worker() {
    while (next < items.length && !stopped()) {
      const item = items[next++];
      try { await perform(source, item); result.confirmed.push(item); }
      catch (error) { result.unconfirmed.push({item, error}); }
      progress(result);
    }
  }
  await Promise.all(Array.from({length: Math.min(3, items.length)}, worker));
  result.unprocessed = items.slice(next);
  return result;
}

window.PrismMailSelection = (() => {
  const state = {selected: new Set(), visible: new Map(), order: [], folder: '', account: '', generation: 0,
    anchor: null, running: null, choosing: false, report: null};
  let host;
  const actionNames = {archive: 'Archive', trash: 'Trash', move: 'Move', read: 'Mark read', unread: 'Mark unread'};
  const updates = count => count + ' update' + (count === 1 ? '' : 's');
  window.addEventListener?.('beforeunload', event => {
    if (state.running) { event.preventDefault(); event.returnValue = ''; }
  });

  function mount() {
    if (host?.isConnected) return host;
    const tabs = document.querySelector('.list-tabs');
    if (!tabs) return null;
    host = document.createElement('div'); host.id = 'mail-selection'; host.className = 'mail-selection';
    host.innerHTML = '<div class="mail-selection-controls"><label class="mail-select-page"><input type="checkbox"><span>This page</span></label>' +
      '<span class="mail-selection-count"></span><div class="mail-selection-actions"></div></div>' +
      '<div class="mail-selection-status"><span role="status" aria-live="polite"></span><button type="button" class="text-btn mail-selection-stop" hidden>Stop</button></div>' +
      '<details class="mail-selection-errors" hidden><summary></summary><ul></ul></details>';
    tabs.after(host);
    host.querySelector('.mail-select-page input').onchange = event => {
      for (const uid of state.order) event.target.checked ? state.selected.add(uid) : state.selected.delete(uid);
      state.anchor = null; state.report = null; update();
    };
    const actions = host.querySelector('.mail-selection-actions');
    for (const [action, label] of Object.entries(actionNames)) {
      const button = document.createElement('button'); button.type = 'button'; button.className = 'btn';
      button.textContent = label; button.dataset.action = action; button.onclick = () => action === 'move' ? chooseMove() : run(action);
      actions.append(button);
    }
    const clear = document.createElement('button'); clear.type = 'button'; clear.className = 'text-btn'; clear.textContent = 'Clear selection';
    clear.dataset.action = 'clear'; clear.onclick = clearSelection; actions.append(clear);
    host.querySelector('.mail-selection-stop').onclick = () => { if (state.running) { state.running.cancelled = true; update(); } };
    return host;
  }
  function currentScope(snapshot) {
    return state.folder === snapshot.folder && state.account === snapshot.account && String(folder) === snapshot.folder;
  }
  function sourceBusy() { return state.running && currentScope(state.running.snapshot); }
  function update() {
    const bar = mount(); if (!bar) return;
    bar.hidden = !state.order.length && !state.running && !state.report;
    const all = bar.querySelector('.mail-select-page input'), count = state.selected.size;
    all.checked = state.order.length > 0 && count === state.order.length;
    all.indeterminate = count > 0 && count < state.order.length;
    all.disabled = !state.order.length || !!sourceBusy() || state.choosing;
    all.setAttribute('aria-label', 'Select all ' + state.order.length + ' messages on this page');
    bar.querySelector('.mail-selection-count').textContent = count ? count + ' selected' : '';
    const actions = bar.querySelector('.mail-selection-actions'); actions.hidden = !count;
    for (const button of actions.querySelectorAll('button')) button.disabled = !!state.running || state.choosing || !state.account;
    // Selecting the next folder does not change the already captured operation.
    const clear = actions.querySelector('[data-action="clear"]');
    clear.disabled = state.choosing || !!sourceBusy();
    const status = bar.querySelector('.mail-selection-status [role="status"]');
    const job = state.running;
    status.textContent = job ? (job.cancelled ? 'Stopping after the current requests… ' : actionNames[job.snapshot.action] + ' · ') +
      job.finished + ' of ' + job.snapshot.items.length + ' processed in ' + job.snapshot.folder : state.report?.text || '';
    bar.querySelector('.mail-selection-status').hidden = !status.textContent;
    const stop = bar.querySelector('.mail-selection-stop'); stop.hidden = !job; stop.disabled = !!job?.cancelled;
    const details = bar.querySelector('.mail-selection-errors'); details.hidden = !state.report?.errors.length;
    if (state.report) {
      details.querySelector('summary').textContent = updates(state.report.errors.length) + ' not confirmed';
      const list = details.querySelector('ul'); list.replaceChildren();
      for (const failure of state.report.errors) {
        const row = document.createElement('li');
        row.textContent = failure.item.subject + ': ' + (failure.error?.message || 'The request could not be confirmed.');
        list.append(row);
      }
    }
    for (const row of document.querySelectorAll('#mlist .mrow[data-uid]')) {
      const selected = state.selected.has(Number(row.dataset.uid));
      row.classList.toggle('mail-selected', selected);
      const input = row.querySelector('.mail-select-message');
      if (input) { input.checked = selected; input.disabled = !!sourceBusy() || state.choosing; }
    }
  }
  function clearSelection() {
    state.selected.clear(); state.anchor = null; state.generation++;
    if (!state.running) state.report = null;
    update();
  }
  function invalidate() {
    if (state.running) { state.running.cancelled = true; state.running.invalidated = true; }
    if (state.choosing && currentScope(state.choosing)) state.choosing.invalidated = true;
    state.order = []; state.visible.clear();
    clearSelection();
  }
  function render(items, options = {}) {
    const account = String(options.account || ''), sourceFolder = String(folder);
    if (account !== state.account || sourceFolder !== state.folder) {
      state.selected.clear(); state.anchor = null; state.generation++; state.report = null;
    }
    state.account = account; state.folder = sourceFolder;
    state.visible = new Map(items.filter(item => Number.isInteger(item.uid) && item.uid > 0 && item.uid <= 0xffffffff).map(item => [item.uid, item]));
    state.order = [...state.visible.keys()];
    for (const uid of state.selected) if (!state.visible.has(uid)) state.selected.delete(uid);
    update();
  }
  function checkbox(message) {
    const input = document.createElement('input'); input.type = 'checkbox'; input.className = 'mail-select-message';
    input.checked = state.selected.has(message.uid); input.disabled = !!sourceBusy() || state.choosing;
    input.setAttribute('aria-label', 'Select ' + (message.subject || 'message without a subject'));
    input.onclick = event => {
      event.stopPropagation();
      if (event.shiftKey && state.anchor != null) {
        const a = state.order.indexOf(state.anchor), b = state.order.indexOf(message.uid);
        if (a >= 0 && b >= 0) for (const uid of state.order.slice(Math.min(a,b), Math.max(a,b)+1)) input.checked ? state.selected.add(uid) : state.selected.delete(uid);
      }
    };
    input.onkeydown = event => { if (event.key === ' ' || event.key === 'Enter') event.stopPropagation(); };
    input.onchange = () => {
      input.checked ? state.selected.add(message.uid) : state.selected.delete(message.uid);
      state.anchor = message.uid; state.report = null; update();
    };
    return input;
  }
  function capture(action, target) {
    return {action, target, folder: state.folder, account: state.account, generation: state.generation,
      items: [...state.selected].filter(uid => state.visible.has(uid)).map(uid => ({uid, subject: String(state.visible.get(uid).subject || '(no subject)')}))};
  }
  async function perform(snapshot, item) {
    if (snapshot.action === 'read' || snapshot.action === 'unread') {
      return PrismAPI.api('/api/email/markseen?folder=' + encodeURIComponent(snapshot.folder), PrismAPI.body('POST', {
        uid: item.uid, seen: snapshot.action === 'read', account: snapshot.account,
      }));
    }
    return mailTool(snapshot.action, {uid: item.uid, folder: snapshot.folder, account: snapshot.account, ...(snapshot.action === 'move' ? {target: snapshot.target} : {})});
  }
  async function run(action, target, captured) {
    if (state.running || state.choosing || !Object.hasOwn(actionNames, action)) return;
    const snapshot = captured || capture(action, target);
    if (snapshot.invalidated || !snapshot.items.length) return;
    if (!snapshot.account) { PrismAPI.toast('Refresh the mailbox before changing selected messages.'); return; }
    if (action === 'move' && (!snapshot.target || snapshot.target === snapshot.folder)) return;
    const job = state.running = {snapshot, cancelled: false, finished: 0}; state.report = null; update();
    const result = await runMailSelectionBatch(snapshot, async (source, item) => {
      try { return await perform(source, item); }
      catch (error) {
        if ([401,403,409].includes(error.status)) job.cancelled = true;
        throw error;
      }
    }, progress => { job.finished = progress.confirmed.length + progress.unconfirmed.length; update(); }, () => job.cancelled);
    state.running = null;
    if (!job.invalidated && currentScope(snapshot) && mailAccount === snapshot.account && current) {
      const changed = result.confirmed.some(item => item.uid === current.uid);
      if (changed && ['archive', 'trash', 'move'].includes(snapshot.action)) resetMailReading();
      else if (changed) current.seen = snapshot.action === 'read';
    }
    if (currentScope(snapshot) && state.generation === snapshot.generation) for (const item of result.confirmed) state.selected.delete(item.uid);
    state.report = {text: snapshot.folder + ': ' + updates(result.confirmed.length) + ' confirmed' +
      (result.unconfirmed.length ? ', ' + result.unconfirmed.length + ' not confirmed' : '') +
      (result.unprocessed.length ? ', ' + result.unprocessed.length + ' not processed' : '') + '.', errors: result.unconfirmed};
    update(); notifyBadge();
    if (result.unconfirmed.length) PrismAPI.toast(updates(result.unconfirmed.length) + ' could not be confirmed. Check the messages before trying again.');
    if (currentScope(snapshot)) {
      try { await refreshMail({preserveReading: true}); }
      catch (_) { PrismAPI.toast('Changes were processed, but the folder could not be refreshed. Refresh it to check the result.'); }
    }
    return result;
  }
  async function chooseMove() {
    if (state.running || state.choosing || !state.selected.size) return;
    const snapshot = capture('move'); state.choosing = snapshot; update();
    try {
      await loadFolders();
      const dialog = mailDialog('Move ' + snapshot.items.length + ' messages from ' + snapshot.folder,
        '<label>Destination<select class="mail-batch-destination">' + folderOptions() + '</select></label><button class="btn btn-accent mail-batch-move">Move messages</button>');
      const select = dialog.querySelector('.mail-batch-destination');
      for (const option of [...select.options]) if (option.value === snapshot.folder) option.remove();
      const move = dialog.querySelector('.mail-batch-move'); move.disabled = !select.options.length;
      dialog.querySelector('.close-dialog').textContent = 'Cancel';
      dialog.onclose = () => { dialog.remove(); state.choosing = false; update(); };
      move.onclick = () => { snapshot.target = select.value; dialog.close(); state.choosing = false; run('move', snapshot.target, snapshot); };
    } catch (error) { state.choosing = false; update(); PrismAPI.toast(error.message); }
  }
  return {render, checkbox, clear: clearSelection, invalidate, busy: () => !!state.running || !!state.choosing, run};
})();
