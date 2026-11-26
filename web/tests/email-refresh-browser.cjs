// Exercise the actual Mail page with a small, fully intercepted mailbox.
const {chromium} = require('playwright');
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
const {createPreferencesBackend} = require('./preferences-fixture.cjs');
const root = path.resolve(__dirname, '..');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

;(async () => {
  const browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_EXECUTABLE_PATH});
  async function fixture(width, count, unread) {
    const context = await browser.newContext({viewport: {width, height: 900}}), page = await context.newPage();
    const errors = [], requests = [], held = [];
    const state = {messages: {INBOX: [], Projects: []}, account: 'synthetic-mail-account', sequence: 1, uidValidity: 10, failList: false, holdList: false};
    function message(uid, folder = 'INBOX') {
      return {uid, folder, from: 'sender@example.test', to: 'owner@example.test', subject: 'Invoice ' + uid,
        date: '2026-11-26T12:00:00Z', seen: !unread.includes(uid),
        body: 'Mail ' + uid + ' contents\n' + Array.from({length: 100}, (_, i) => 'Line ' + i + ': synthetic text for reading and scrolling.').join('\n')};
    }
    state.messages.INBOX = Array.from({length: count}, (_, i) => message(i + 1));
    state.messages.Projects = [message(51, 'Projects')];
    const html = state.messages.INBOX.find(item => item.uid === 7);
    if (html) html.body = '<html><body><p>Synthetic HTML message</p><img alt="Synthetic inline image" src="data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="></body></html>';
    page.on('pageerror', error => errors.push(error.message));
    const prefs = createPreferencesBackend({origin: 'http://mail-refresh.invalid'});
    await context.route('**/*', async route => {
      const url = new URL(route.request().url());
      assert.equal(url.origin, 'http://mail-refresh.invalid', 'Mail contacted an external service');
      const preferences = prefs.handle(route); if (preferences) return preferences;
      const reply = (body, status = 200) => route.fulfill({status, contentType: 'application/json', body: JSON.stringify(body)});
      if (url.pathname.startsWith('/api/')) requests.push({path: url.pathname, folder: url.searchParams.get('folder'), q: url.searchParams.get('q'),
        unread: url.searchParams.get('unread'), offset: Number(url.searchParams.get('offset') || 0)});
      const source = url.searchParams.get('folder') || 'INBOX';
      if (url.pathname === '/api/email/config') return reply({configured: true, has_password: true, user: 'owner@example.test', list_limit: 2});
      if (url.pathname === '/api/email/tags') return reply({});
      if (url.pathname === '/api/email/drafts') return reply({drafts: []});
      if (url.pathname === '/api/ai/assist') {
        assert.equal(route.request().method(), 'POST');
        assert.equal(route.request().postDataJSON().task, 'email_summary');
        return reply({result: 'Synthetic summary of the selected message.'});
      }
      if (url.pathname === '/api/builtin/email') return reply({result: [{name: 'INBOX', role: 'inbox', selectable: true}, {name: 'Projects', selectable: true}]});
      if (url.pathname === '/api/email/status') {
        const messages = state.messages[source] || [];
        return reply({folder: source, messages: messages.length, unread: messages.filter(item => !item.seen).length,
          uid_next: Math.max(0, ...messages.map(item => item.uid)) + 1, uid_validity: state.uidValidity, highest_modseq: state.sequence, account: state.account});
      }
      if (['/api/email/list', '/api/email/search'].includes(url.pathname)) {
        if (state.failList) { state.failList = false; return reply({error: 'Synthetic mailbox is temporarily unavailable.'}, 503); }
        const messages = state.messages[source] || [], term = (url.searchParams.get('q') || '').toLowerCase();
        const onlyUnread = ['true','1'].includes(url.searchParams.get('unread'));
        const matching = messages.filter(item => (!onlyUnread || !item.seen) && (!term || item.subject.toLowerCase().includes(term))).sort((a,b) => b.uid - a.uid);
        const offset = Number(url.searchParams.get('offset') || 0), limit = Number(url.searchParams.get('limit') || 2);
        const body = {messages: matching.slice(offset, offset + limit), total: matching.length, offset, limit,
          next_offset: offset + limit < matching.length ? offset + limit : null, unread_count: messages.filter(item => !item.seen).length,
          folder_total: messages.length, account: state.account};
        if (state.holdList && source === 'INBOX') { state.holdList = false; await new Promise(resolve => held.push(resolve)); }
        return reply(body);
      }
      if (url.pathname === '/api/email/read') return reply({message: state.messages[source].find(item => item.uid === Number(url.searchParams.get('uid')))});
      if (url.pathname === '/api/email/markseen') {
        const body = route.request().postDataJSON();
        if (body.account !== state.account) return reply({error: 'Mailbox changed.'}, 409);
        const item = state.messages[source].find(item => item.uid === body.uid); item.seen = body.seen; state.sequence++;
        return reply({});
      }
      if (url.pathname.startsWith('/api/')) return reply({});
      const file = path.join(root, url.pathname);
      return fs.existsSync(file) && fs.statSync(file).isFile() ? route.fulfill({path: file}) : route.fulfill({status: 404, body: ''});
    });
    await page.goto('http://mail-refresh.invalid/apps/email.html');
    await page.waitForFunction(() => !mailListLoading && messages.length === 2);
    return {page, context, state, requests, errors, held, message,
      count: pathname => requests.filter(request => request.path === pathname).length,
      close: async () => { held.splice(0).forEach(resolve => resolve()); await context.close(); }};
  }
  try {
    // Unread mail lives beyond the initial page. Search and pagination must
    // preserve the server filter, rather than hiding rows from the first page.
    const first = await fixture(1280, 12, [1,2,3,4,5]);
    try {
      assert.match(await first.page.locator('#mail-count').textContent(), /12 messages.*5 unread/);
      assert.equal(await first.page.locator('.mrow[data-uid="1"]').count(), 0);
      await first.page.getByRole('button', {name: 'Unread', exact: true}).click();
      await first.page.locator('.mrow[data-uid="5"]').waitFor();
      assert(first.requests.some(request => request.path === '/api/email/list' && request.unread === 'true'));
      assert.match(await first.page.locator('#page-label').textContent(), /1–2 of 5/);
      await first.page.locator('#search').fill('Invoice'); await first.page.locator('#search').press('Enter');
      await first.page.waitForFunction(() => !mailListLoading && searchQuery === 'Invoice');
      await first.page.getByRole('button', {name: 'Older messages', exact: true}).click();
      await first.page.locator('.mrow[data-uid="3"]').waitFor();
      assert(first.requests.some(request => request.path === '/api/email/search' && request.offset === 2 && request.q === 'Invoice' && request.unread === 'true'));
      assert.match(await first.page.locator('#page-label').textContent(), /3–4 of 5/);
      await first.page.locator('.mrow[data-uid="3"]').click();
      await first.page.locator('.rbody').waitFor();
      await first.page.evaluate(() => { window.__unreadReader = document.querySelector('.rbody'); });
      const marked = Date.now() + 5000;
      while (!first.state.messages.INBOX.find(item => item.uid === 3).seen && Date.now() < marked) await delay(20);
      assert.equal(first.state.messages.INBOX.find(item => item.uid === 3).seen, true);
      await first.page.evaluate(() => refreshMail({preserveReading: true}));
      assert.equal(await first.page.locator('.mrow[data-uid="3"]').count(), 0);
      assert.equal(await first.page.evaluate(() => current.uid === 3 && document.querySelector('.rbody') === window.__unreadReader), true,
        'Marking an opened message read removed its reader with the unread result');
      assert.deepEqual(first.errors, []);
      console.log('Mail refresh: old unread messages, folder-wide counts and paginated filtered search passed.');
    } finally { await first.close(); }

    for (const width of [1280, 390]) {
      const f = await fixture(width, 8, [1,2]);
      try {
        await f.page.locator('.mrow[data-uid="8"]').click();
        await f.page.locator('.rbody').waitFor();
        await f.page.getByRole('button', {name: 'Summarize', exact: true}).click();
        await f.page.locator('.ai-box').filter({hasText: 'Synthetic summary of the selected message.'}).waitFor();
        const scroll = await f.page.evaluate(() => {
          window.__reader = document.querySelector('.rbody'); window.__reader.scrollTop = 150; return window.__reader.scrollTop;
        });
        assert(scroll > 0, 'The reader must actually scroll for this check');
        await f.page.waitForFunction(() => !mailPollRunning && !mailListLoading);
        const listBefore = f.count('/api/email/list'), readsBefore = f.count('/api/email/read');
        await f.page.evaluate(() => checkMailUpdates());
        assert.equal(f.count('/api/email/list'), listBefore, 'Unchanged STATUS fetched full message pages');
        assert.equal(f.count('/api/email/read'), readsBefore, 'Unchanged STATUS reloaded the reader');
        f.state.messages.INBOX.push(f.message(9)); f.state.sequence++;
        await f.page.evaluate(() => checkMailUpdates());
        assert.equal(f.count('/api/email/list'), listBefore + 1, 'Changed STATUS should fetch one page');
        assert.equal(f.count('/api/email/read'), readsBefore);
        assert.equal(await f.page.evaluate(() => document.querySelector('.rbody') === window.__reader && window.__reader.scrollTop === 150), true);

        f.state.failList = true;
        await f.page.evaluate(() => refreshMail({preserveReading: true}));
        assert.equal(await f.page.evaluate(() => document.querySelector('.rbody') === window.__reader && window.__reader.scrollTop === 150), true);
        assert.match(await f.page.locator('#mail-refresh-status').textContent(), /Refresh failed/);
        assert.equal(await f.page.locator('.mrow').count(), 2, 'A failed refresh replaced the mailbox with an empty list');

        // HTML messages retain the same iframe and inline image across refreshes.
        await f.page.evaluate(() => openMessage(7));
        await f.page.locator('.rhtml').waitFor();
        await f.page.evaluate(() => { window.__htmlReader = document.querySelector('.rhtml'); });
        const htmlReads = f.count('/api/email/read');
        await f.page.evaluate(() => refreshMail({preserveReading: true}));
        assert.equal(await f.page.evaluate(() => document.querySelector('.rhtml') === window.__htmlReader), true);
        assert.equal(f.count('/api/email/read'), htmlReads);

        // A reset changes what the same UID identifies. Neither the old reader
        // nor a selection may survive into the replacement namespace.
        if (width < 760) await f.page.getByRole('button', {name: 'Back to messages', exact: true}).click();
        await f.page.locator('.mrow[data-uid="8"] .mail-select-message').check();
        f.state.uidValidity = 11; f.state.sequence++;
        const reused = f.state.messages.INBOX.find(item => item.uid === 7);
        reused.subject = 'Replacement message with reused UID'; reused.body = 'This belongs to the new UID namespace.';
        await f.page.evaluate(() => checkMailUpdates());
        await f.page.waitForFunction(() => !mailPollRunning && !mailListLoading);
        assert.equal(await f.page.evaluate(() => current), null, 'A reset left the old UID open as if it belonged to the replacement namespace');
        assert.equal(await f.page.locator('.rhtml').count(), 0);
        assert.equal(await f.page.locator('.mail-select-message:checked').count(), 0, 'A reset preserved a selection of old UIDs');

        // A late response from the previous folder must not replace the new one.
        f.state.holdList = true;
        await f.page.evaluate(() => { window.__heldRefresh = refreshMail({preserveReading: true}); });
        const until = Date.now() + 5000;
        while (!f.held.length && Date.now() < until) await delay(20);
        assert.equal(f.held.length, 1);
        if (width < 960) await f.page.getByRole('button', {name: 'Show folders', exact: true}).click();
        await f.page.locator('#folder-nav .nav-item').filter({hasText: 'Projects'}).click();
        await f.page.locator('.mrow[data-uid="51"]').waitFor();
        f.held.splice(0).forEach(resolve => resolve());
        await f.page.evaluate(() => window.__heldRefresh);
        assert.equal(await f.page.locator('.mrow').count(), 1);
        assert.equal(await f.page.locator('#folder-title').textContent(), 'Projects');
        assert.equal(await f.page.locator('.mrow[data-uid="51"]').count(), 1);
        assert.deepEqual(f.errors, []);
        console.log('Mail refresh ' + width + ': summary, reader DOM/scroll, HTML iframe, UID namespace reset, cheap polling, partial outage and stale-folder responses passed.');
      } finally { await f.close(); }
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
