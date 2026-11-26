// Mail views use the server's folder-wide counts and keep reading independent
// from list refreshes. No message contents or drafts are stored in the browser.
let mailAccount = '', mailPagination = null, mailListLoading = false;
let mailStatusStamp = '', mailPollRunning = false, mailUpdateTimer = null, mailRefreshTimer = null;
const mailUIDValidities = new Map();

function resetMailReading() {
  ++readingGeneration;
  current = null;
  document.getElementById('mail')?.classList.remove('reading-open');
  const reading = document.getElementById('reading');
  if (reading) reading.innerHTML = emptyReading();
}
function mailRefreshStatus(text = '', error = false) {
  const label = document.getElementById('mail-refresh-status');
  if (label) { label.textContent = text; label.classList.toggle('mail-sync-error', error); }
}
function mailboxStatusStamp(status) {
  if (!status || !Number.isFinite(status.messages) || !Number.isFinite(status.uid_validity)) return '';
  return JSON.stringify([status.account || '', status.uid_validity, status.uid_next, status.messages, status.unread, status.highest_modseq || 0]);
}
async function readMailboxStatus(source) {
  try { return await PrismAPI.api('/api/email/status?folder=' + encodeURIComponent(source)); }
  catch (_) { return null; } // Manual loading still works if STATUS is unavailable.
}
function mailAccountQuery(account = mailAccount) { return account ? '&account=' + encodeURIComponent(account) : ''; }

function rememberMailUIDValidity(status, source) {
  if (!status?.account || status.folder !== source || !Number.isInteger(status.uid_validity) || status.uid_validity < 1 || status.uid_validity > 0xffffffff) return;
  const key = JSON.stringify([status.account, source]), previous = mailUIDValidities.get(key);
  mailUIDValidities.set(key, status.uid_validity);
  if (status.account !== mailAccount || previous == null || previous === status.uid_validity) return;
  // Recreated IMAP folders can reuse old UIDs for different messages. Those
  // cached rows and the reader are invalid even when the next fetch fails.
  resetMailReading();
  window.PrismMailSelection?.invalidate();
  messages = []; mailPagination = null;
  setList('<div class="placeholder" style="padding:30px">Loading…</div>');
}

async function requestMailPage(query, keepOffset = false, options = {}) {
  if (!keepOffset) mailOffset = 0;
  const generation = ++mailLoadGeneration, source = folder, requestedUnread = unreadOnly;
  const preserveReading = options.preserveReading ?? keepOffset;
  const list = document.getElementById('mlist');
  const preserveScroll = options.preserveScroll ?? (keepOffset && !options.pageChanged);
  if (!preserveReading) resetMailReading();
  if (!keepOffset || options.pageChanged) window.PrismMailSelection?.clear();
  mailListLoading = true;
  list?.setAttribute('aria-busy', 'true');
  if (!options.quiet) mailRefreshStatus('Refreshing…');
  if (!messages.length || options.pageChanged || !preserveReading) setList('<div class="placeholder" style="padding:30px">Loading…</div>');
  const stillCurrent = () => generation === mailLoadGeneration && source === folder && requestedUnread === unreadOnly && query === searchQuery;
  try {
    // Take the lightweight stamp before fetching messages: an arrival during
    // the list request must be detectable on the next check.
    const status = options.status || await readMailboxStatus(source);
    if (!stillCurrent()) return false;
    rememberMailUIDValidity(status, source);
    const limit = inboxLimit();
    const pageURL = () => '/api/email/' + (query ? 'search' : 'list') + '?folder=' + encodeURIComponent(source) +
      '&limit=' + limit + '&offset=' + mailOffset + (requestedUnread ? '&unread=true' : '') + (query ? '&q=' + encodeURIComponent(query) : '');
    let data = await PrismAPI.api(pageURL());
    if (!stillCurrent()) return false;
    if (!data.messages?.length && mailOffset > 0 && Number.isFinite(data.total)) {
      mailOffset = data.total > 0 ? Math.floor((data.total - 1) / limit) * limit : 0;
      data = await PrismAPI.api(pageURL());
      if (!stillCurrent()) return false;
    }
    const account = typeof data.account === 'string' ? data.account : mailAccount;
    const changedAccount = !!mailAccount && !!account && account !== mailAccount;
    let settings = cfg;
    if (changedAccount) {
      settings = await PrismAPI.api('/api/email/config');
      if (!stillCurrent()) return false;
    }
    const tags = await PrismAPI.api('/api/email/tags?folder=' + encodeURIComponent(source) + mailAccountQuery(account));
    if (!stillCurrent()) return false;
    if (changedAccount) { resetMailReading(); window.PrismMailSelection?.clear(); cfg = settings; }
    const next = data.messages || [];
    const redraw = JSON.stringify(messages) !== JSON.stringify(next) || JSON.stringify(mailTags) !== JSON.stringify(tags);
    messages = next; mailTags = tags; mailAccount = account;
    mailPagination = {
      total: Number.isFinite(data.total) ? data.total : null,
      unread_count: Number.isFinite(data.unread_count) ? data.unread_count : null,
      folder_total: Number.isFinite(data.folder_total) ? data.folder_total : null,
      next_offset: Object.hasOwn(data, 'next_offset') ? data.next_offset : (next.length === limit ? mailOffset + limit : null),
    };
    if (redraw || !preserveReading || options.pageChanged || !list?.querySelector('.mrow')) {
      const scroll = list?.scrollTop || 0;
      const active = document.activeElement, focused = active?.closest?.('.mrow')?.dataset.uid;
      const focusedCheckbox = active?.matches?.('input[type="checkbox"]');
      renderList();
      if (preserveScroll && list) list.scrollTop = scroll;
      if (focused && !options.pageChanged) {
        const row = [...(list?.querySelectorAll('.mrow') || [])].find(row => row.dataset.uid === focused);
        (focusedCheckbox ? row?.querySelector('input[type="checkbox"]') : row)?.focus({preventScroll: true});
      }
    } else updateMailHeading();
    document.getElementById('older-mail').disabled = mailPagination.next_offset == null;
    document.getElementById('newer-mail').disabled = mailOffset === 0;
    if (status?.account === account || !account) mailStatusStamp = mailboxStatusStamp(status);
    else mailStatusStamp = '';
    const accountLabel = document.querySelector('.mail-account');
    if (changedAccount) {
      if (accountLabel) { accountLabel.textContent = cfg.user || 'Connected mailbox'; accountLabel.title = cfg.user || ''; }
      loadFolders();
    }
    mailRefreshStatus();
    if (!draftIsOpen()) publishMailContext();
    return true;
  } catch (error) {
    if (stillCurrent()) {
      mailRefreshStatus('Refresh failed. Try again.', true);
      if (!messages.length || !preserveReading || options.pageChanged) setList(errBox(error.message));
      if (!options.quiet) PrismAPI.toast('Could not refresh mail: ' + error.message);
    }
    return false;
  } finally {
    if (generation === mailLoadGeneration) { mailListLoading = false; list?.setAttribute('aria-busy', 'false'); }
  }
}

function loadInbox(keepOffset = false, options = {}) {
  searchQuery = '';
  return requestMailPage('', keepOffset, options);
}
function search(query, keepOffset = false, options = {}) {
  searchQuery = query;
  return requestMailPage(query, keepOffset, options);
}
function browseMailPage(offset) {
  mailOffset = Math.max(0, offset);
  return searchQuery ? search(searchQuery, true, {preserveReading: true, pageChanged: true}) : loadInbox(true, {preserveReading: true, pageChanged: true});
}
function queueMailRefresh() {
  clearTimeout(mailRefreshTimer);
  mailRefreshTimer = setTimeout(() => {
    if (document.hidden || draftIsOpen() || window.PrismMailSelection?.busy()) return;
    refreshMail({preserveReading: true, quiet: true});
  }, 250);
}
async function checkMailUpdates() {
  if (document.hidden || !document.getElementById('mail') || !cfg?.configured || mailListLoading || mailPollRunning || draftIsOpen() || window.PrismMailSelection?.busy()) return;
  mailPollRunning = true;
  const source = folder, generation = mailLoadGeneration;
  try {
    const status = await readMailboxStatus(source);
    if (source !== folder || generation !== mailLoadGeneration || document.hidden) return;
    const stamp = mailboxStatusStamp(status);
    if (!stamp) { mailRefreshStatus('Could not check for new mail. Refresh to retry.', true); return; }
    if (stamp !== mailStatusStamp) await refreshMail({preserveReading: true, quiet: true, status});
  } finally { mailPollRunning = false; }
}
function startMailUpdates() {
  if (mailUpdateTimer) return;
  mailUpdateTimer = setInterval(checkMailUpdates, 30000);
  addEventListener('focus', checkMailUpdates);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) checkMailUpdates(); });
  addEventListener('pagehide', () => { clearInterval(mailUpdateTimer); clearTimeout(mailRefreshTimer); mailUpdateTimer = null; }, {once: true});
}
