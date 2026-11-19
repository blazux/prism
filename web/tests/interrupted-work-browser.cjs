const {chromium} = require('playwright')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const root = path.resolve(__dirname, '..'), origin = 'http://fixture.invalid'

;(async () => {
  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_EXECUTABLE_PATH ? {executablePath: process.env.CHROMIUM_EXECUTABLE_PATH} : {})})
  try {
    for (const multi of [false, true]) {
      const context = await browser.newContext({viewport: {width: 1100, height: 820}})
      const page = await context.newPage(), errors = []
      const session = multi ? 'u7-main' : 'main'
      page.on('pageerror', error => errors.push(error.message))
      await page.addInitScript(() => {
        window.fixtureSockets = []
        window.WebSocket = class {
          static OPEN = 1; static CLOSED = 3
          constructor(url) {this.url = url; this.readyState = 1; this.messages = []; fixtureSockets.push(this); queueMicrotask(() => this.onopen?.())}
          send(value) {this.messages.push(JSON.parse(value))}
          close() {this.readyState = 3; this.onclose?.()}
        }
        window.deliverFixture = message => fixtureSockets.at(-1).onmessage({data: JSON.stringify(message)})
      })
      await page.route('**/*', async route => {
        const url = new URL(route.request().url())
        assert.equal(url.origin, origin, 'fixture requested an external resource')
        if (url.pathname === '/app.js') {
          return route.fulfill({contentType: 'text/javascript', body: fs.readFileSync(path.join(root, 'app.js'), 'utf8') + '\nwindow.fixtureRun={resume:resumeInterruptedChat,get open(){return chatOpen},get session(){return currentSessionID},get files(){return pendingFiles},set files(value){pendingFiles=value}};'})
        }
        if (!url.pathname.startsWith('/api/')) {
          const file = path.resolve(root, url.pathname === '/' ? 'index.html' : '.' + url.pathname)
          return file.startsWith(root + path.sep) && fs.existsSync(file) && fs.statSync(file).isFile() ? route.fulfill({path: file}) : route.fulfill({status: 404, body: ''})
        }
        let data = {}, status = 200
        if (url.pathname === '/api/auth') data = {authenticated: true, auth_enabled: false}
        else if (url.pathname === '/api/me') data = {authenticated: true, multiUser: multi, isAdmin: !multi, user: multi ? {id: 7, role: 'member'} : undefined}
        else if (url.pathname === '/api/profile') data = {userId: multi ? 7 : 0, displayName: 'Fixture'}
        else if (url.pathname === '/api/platform') data = {disabledApps: []}
        else if (url.pathname === '/api/my/groups') data = {groups: []}
        else if (url.pathname === '/api/agent/name') data = {name: 'Fixture'}
        else if (url.pathname === '/api/models') data = {models: ['fixture']}
        else if (url.pathname === '/api/vox-workspace') data = {available: false}
        else if (url.pathname === '/api/sessions') data = {sessions: [{id: session, name: 'Report', runStatus: 'interrupted'}]}
        else if (url.pathname === '/api/sessions/' + session + '/activity') data = {session, runStatus: 'interrupted', messages: [], recentTools: [], subagents: [], backgroundJobs: 0}
        else if (url.pathname === '/api/activity') data = {events: []}
        else if (url.pathname === '/api/resources') data = []
        else if (url.pathname.startsWith('/api/sessions/') && url.pathname.endsWith('/activity')) {status = 404; data = {error: 'not found'}}
        return route.fulfill({status, contentType: 'application/json', body: JSON.stringify(data)})
      })
      await page.goto(origin)
      await page.waitForFunction(() => fixtureSockets.length > 0)
      await page.locator('.rail-item[data-app="activity"]').click()
      const activity = page.frameLocator('#app-frame')
      await activity.getByRole('button', {name: 'Interrupted', exact: true}).click()
      await activity.getByText('Report', {exact: true}).waitFor()
      await activity.getByRole('link', {name: 'Open chat'}).click()
      await page.waitForFunction(id => fixtureRun.open && fixtureRun.session === id, session)
      const calls = () => page.evaluate(() => fixtureSockets.flatMap(socket => socket.messages).filter(message => message.type === 'chat'))
      assert.equal((await calls()).length, 0, 'opening an interrupted conversation must not resume it')
      await page.locator('#chat-input').fill('My separate draft')
      await page.evaluate(() => {
        fixtureRun.files = [{name: 'keep.txt', path: 'uploads/keep.txt'}]
        deliverFixture({type: 'chat_history', messages: [{role: 'user', content: 'Prepare the report'}, {role: 'assistant', content: 'Saved partial response'}]})
        deliverFixture({type: 'run_status', status: 'interrupted'})
      })
      await page.locator('#chat-delivery-status').getByRole('button', {name: 'Resume', exact: true}).click()
      await page.evaluate(() => fixtureRun.resume())
      const sent = await calls()
      assert.equal(sent.length, 1, 'double click must not submit twice')
      assert.equal(sent[0].content, 'Continue.')
      assert.equal(sent[0].files, undefined)
      assert.equal(await page.locator('#chat-input').inputValue(), 'My separate draft')
      await page.evaluate(id => {
        deliverFixture({type: 'chat_receipt', requestId: id, status: 'accepted'})
        deliverFixture({type: 'run_status', status: 'running'})
      }, sent[0].requestId)
      assert.equal(await page.locator('#chat-input').inputValue(), 'My separate draft')
      assert.equal(await page.evaluate(() => fixtureRun.files[0].path), 'uploads/keep.txt')
      assert.equal(await page.locator('#chat-delivery-status button').count(), 0)
      await page.reload()
      await page.waitForFunction(() => fixtureSockets.length > 0)
      await page.evaluate(() => deliverFixture({type: 'run_status', status: 'interrupted'}))
      assert.equal((await calls()).length, 0, 'reloading must not resume automatically')
      await page.setViewportSize({width: 390, height: 820})
      await page.locator('.rail-item[data-app="activity"]').click()
      await activity.getByRole('button', {name: 'Interrupted', exact: true}).click()
      await activity.getByRole('link', {name: 'Open chat'}).waitFor()
      assert.equal(await page.locator('#app-frame').evaluate(frame => frame.contentDocument.documentElement.scrollWidth <= frame.contentWindow.innerWidth), true)
      assert.deepEqual(errors, [])
      await context.close()
    }
    console.log('Interrupted work: mono/multi Activity links, explicit resume, draft preservation, duplicate guard, reload and mobile passed')
  } finally {await browser.close()}
})().catch(error => {console.error(error); process.exitCode = 1})
