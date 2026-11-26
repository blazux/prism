// Actual Mail UI, synthetic mailbox only. All network requests are intercepted.
const {chromium} = require('playwright');
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
const {createPreferencesBackend} = require('./preferences-fixture.cjs');
const root = path.resolve(__dirname, '..');
const tick = () => new Promise(resolve => setTimeout(resolve, 20));

;(async () => {
  const browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_EXECUTABLE_PATH});
  try {
    for (const width of [1280, 390]) {
      const context = await browser.newContext({viewport: {width, height: 900}}), page = await context.newPage();
      const errors = [], writes = [], waiting = [], messages = {INBOX: [], Projects: []};
      let account = 'synthetic-mailbox-A', hold = false, active = 0, maximum = 0, reads = 0;
      for (const [folder, ids] of Object.entries({INBOX: Array.from({length: 12}, (_, i) => i + 1), Projects: [51,52]})) {
        messages[folder] = ids.map(uid => ({folder, uid, from: 'Sender <sender@example.test>', to: 'owner@example.test',
          subject: 'Synthetic message ' + uid, date: '2026-11-26T12:00:00Z', seen: false, body: 'Synthetic body ' + uid}));
      }
      page.on('pageerror', error => errors.push(error.message));
      const prefs = createPreferencesBackend({origin: 'http://mail.invalid'});
      await context.route('**/*', async route => {
        const url = new URL(route.request().url());
        assert.equal(url.origin, 'http://mail.invalid', 'Mail contacted an external service');
        const preference = prefs.handle(route); if (preference) return preference;
        const reply = (value, status = 200) => route.fulfill({status, contentType: 'application/json', body: JSON.stringify(value)});
        const source = url.searchParams.get('folder') || 'INBOX';
        if (url.pathname === '/api/email/config') return reply({configured: true, has_password: true, user: 'owner@example.test', list_limit: 60});
        if (url.pathname === '/api/email/tags') return reply({});
        if (url.pathname === '/api/email/drafts') return reply({drafts: []});
        if (url.pathname === '/api/email/list') {
          const all = messages[source] || [], unread = ['true','1'].includes(url.searchParams.get('unread'));
          const filtered = unread ? all.filter(message => !message.seen) : all;
          const offset = Number(url.searchParams.get('offset') || 0), limit = Number(url.searchParams.get('limit') || 60);
          return reply({messages: filtered.slice(offset, offset + limit), total: filtered.length, offset, limit,
            next_offset: offset + limit < filtered.length ? offset + limit : null, unread_count: all.filter(message => !message.seen).length,
            folder_total: all.length, account});
        }
        if (url.pathname === '/api/email/read') { reads++; return reply({message: messages[source].find(message => message.uid === Number(url.searchParams.get('uid')))}); }
        if (url.pathname === '/api/builtin/email' || url.pathname === '/api/email/markseen') {
          const body = route.request().postDataJSON();
          if (body.action === 'folders') return reply({result: [{name: 'INBOX', role: 'inbox', selectable: true}, {name: 'Projects', selectable: true}]});
          const operation = {...body, folder: body.folder || source}; writes.push(operation);
          if (body.account !== account) return reply({error: 'Mailbox changed. Refresh before changing its messages.'}, 409);
          active++; maximum = Math.max(maximum, active);
          if (hold) await new Promise(resolve => waiting.push(resolve));
          active--;
          if (body.uid === 2 && body.action === 'archive') return reply({error: 'Synthetic request was not confirmed.'}, 503);
          const mailbox = messages[operation.folder] || [], message = mailbox.find(item => item.uid === body.uid);
          if (!message) return reply({error: 'Synthetic message no longer exists.'}, 404);
          if (url.pathname === '/api/email/markseen') message.seen = body.seen;
          else if (['archive','trash','move'].includes(body.action)) {
            messages[operation.folder] = mailbox.filter(item => item.uid !== body.uid);
            if (body.action === 'move') messages[body.target].push({...message, folder: body.target, uid: 1000 + body.uid});
          }
          return reply(url.pathname === '/api/email/markseen' ? {} : {result: {ok: true}});
        }
        if (url.pathname.startsWith('/api/')) return reply({});
        const file = path.join(root, url.pathname);
        return fs.existsSync(file) && fs.statSync(file).isFile() ? route.fulfill({path: file}) : route.fulfill({status: 404, body: ''});
      });
      try {
        await page.goto('http://mail.invalid/apps/email.html');
        await page.locator('.mrow[data-uid="1"] .mail-select-message').waitFor();
        const firstCheckbox = page.locator('.mrow[data-uid="1"] .mail-select-message');
        await firstCheckbox.focus(); await page.keyboard.press('Space');
        assert.equal(await firstCheckbox.isChecked(), true);
        await page.keyboard.press('Space');
        assert.equal(await firstCheckbox.isChecked(), false);
        await firstCheckbox.check();
        await page.locator('.mrow[data-uid="3"] .mail-select-message').click({modifiers: ['Shift']});
        assert.equal(await page.locator('.mrow[data-uid="2"] .mail-select-message').isChecked(), true, 'Shift selection skipped an intermediate message');
        assert.equal(reads, 0, 'Selecting a message opened its body');
        await page.locator('.mrow[data-uid="3"]').click();
        await page.locator('.rbody').waitFor();
        assert.equal(await page.evaluate(() => current.uid), 3);
        if (width < 760) await page.getByRole('button', {name: 'Back to messages', exact: true}).click();
        await page.locator('#mail-selection [data-action="archive"]').click();
        await page.waitForFunction(() => !PrismMailSelection.busy() && !mailListLoading);
        await page.locator('.mrow[data-uid="1"]').waitFor({state: 'detached'});
        assert.equal(await page.locator('.mrow[data-uid="2"] .mail-select-message').isChecked(), true);
        assert.match(await page.locator('.mail-selection-status').textContent(), /2 updates confirmed, 1 not confirmed/);
        assert.equal(writes.filter(write => write.action === 'archive').length, 3, 'An unconfirmed side effect was retried');
        assert.equal(await page.evaluate(() => current), null, 'The successfully archived message remained open');
        assert.equal(await page.locator('.rbody').count(), 0);
        assert(writes.every(write => write.folder === 'INBOX' && write.account === 'synthetic-mailbox-A'));
        assert(maximum <= 3);

        await page.locator('#mail-selection [data-action="clear"]').click();
        await page.locator('.mrow[data-uid="6"]').click();
        await page.locator('.rbody').waitFor();
        await page.evaluate(() => { window.__keptReader = document.querySelector('.rbody'); });
        if (width < 760) await page.getByRole('button', {name: 'Back to messages', exact: true}).click();
        await page.locator('.mrow[data-uid="8"] .mail-select-message').check();
        await page.locator('#mail-selection [data-action="archive"]').click();
        await page.waitForFunction(() => !PrismMailSelection.busy() && !mailListLoading);
        assert.equal(await page.evaluate(() => current?.uid === 6 && document.querySelector('.rbody') === window.__keptReader), true,
          'Archiving another message replaced the current reader');
        await page.locator('.mrow[data-uid="7"] .mail-select-message').check();
        await page.locator('#mail-selection [data-action="move"]').click();
        await page.locator('.mail-batch-destination').selectOption('Projects');
        await page.locator('.mail-batch-move').click();
        await page.waitForFunction(() => !PrismMailSelection.busy() && !mailListLoading);
        assert.equal(await page.locator('.mrow[data-uid="7"]').count(), 0);
        assert.equal(await page.evaluate(() => current?.uid === 6 && document.querySelector('.rbody') === window.__keptReader), true,
          'Moving another message replaced the current reader after refresh');

        await page.locator('.mrow[data-uid="9"] .mail-select-message').check();
        await page.locator('#mail-selection [data-action="move"]').click();
        await page.locator('.mail-batch-destination').selectOption('Projects');
        const beforeInvalidatedMove = writes.length;
        await page.evaluate(() => PrismMailSelection.invalidate());
        await page.locator('.mail-batch-move').click();
        await page.waitForFunction(() => !PrismMailSelection.busy());
        assert.equal(writes.length, beforeInvalidatedMove, 'A Move dialog reused message UIDs after invalidation');
        assert.equal(await page.locator('.mail-select-page input').isDisabled(), true, 'Invalidated visible UIDs could be selected again before a fresh page loaded');
        await page.evaluate(() => loadInbox());

        const beforeFolderSwitchMove = writes.length;
        for (const uid of [4,5]) await page.locator('.mrow[data-uid="' + uid + '"] .mail-select-message').check();
        await page.locator('#mail-selection [data-action="move"]').click();
        await page.locator('.mail-batch-destination').selectOption('Projects');
        hold = true;
        await page.locator('.mail-batch-move').click();
        while (waiting.length < 2) await tick();
        if (width < 960) await page.getByRole('button', {name: 'Show folders', exact: true}).click();
        await page.locator('#folder-nav .nav-item').filter({hasText: 'Projects'}).click();
        await page.locator('.mrow[data-uid="51"] .mail-select-message').check();
        hold = false; waiting.splice(0).forEach(resolve => resolve());
        await page.waitForFunction(() => !PrismMailSelection.busy());
        assert.equal(await page.locator('.mrow[data-uid="51"] .mail-select-message').isChecked(), true, 'The old operation erased a new folder selection');
        assert(writes.slice(beforeFolderSwitchMove).every(write => write.folder === 'INBOX' && write.account === 'synthetic-mailbox-A' && write.target === 'Projects'));

        if (width < 960) await page.getByRole('button', {name: 'Show folders', exact: true}).click();
        await page.locator('#folder-nav .nav-item').filter({hasText: 'Inbox'}).click();
        await page.locator('.mrow[data-uid="6"] .mail-select-message').waitFor();
        await page.locator('.mail-select-page input').check();
        const before = writes.length;
        const previousSeen = messages.INBOX.map(message => [message.uid, message.seen]);
        account = 'synthetic-mailbox-B';
        await page.locator('#mail-selection [data-action="read"]').click();
        await page.waitForFunction(() => !PrismMailSelection.busy());
        assert(writes.length - before <= 3, 'A changed mailbox did not stop the remaining queue');
        assert.deepEqual(messages.INBOX.map(message => [message.uid, message.seen]), previousSeen, 'An old selection changed the new mailbox');
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'Selection controls overflow the viewport');
        assert.deepEqual(errors, []);
        console.log('Mail selection ' + width + ': partial results, current-reader removal/preservation, bounded concurrency, source snapshots, account guard and folder-switch selection passed.');
      } finally { hold = false; waiting.splice(0).forEach(resolve => resolve()); await context.close(); }
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
