// Exercises the shipped browser helpers through real Go handlers and an
// isolated PostgreSQL schema. No model or installed Prism instance is used.
const {chromium} = require('playwright')
const assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), os = require('node:os'), childProcess = require('node:child_process')
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
const repo = path.resolve(__dirname, '../..')

;(async () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'prism-preferences-browser-'))
  const ready = process.env.PRISM_PREFERENCES_FIXTURE_READY || path.join(temporary, 'ready.json')
  const binary = process.env.PRISM_PREFERENCES_FIXTURE_BINARY
  const fixture = process.env.PRISM_PREFERENCES_FIXTURE_READY ? null : childProcess.spawn(binary || 'go', binary
    ? ['-test.run=^TestPreferencesBrowserFixture$', '-test.v', '-test.timeout=180s']
    : ['test', '-count=1', '-run', '^TestPreferencesBrowserFixture$', './internal/server', '-v', '-timeout', '180s'], {
      cwd: repo, env: {...process.env, PRISM_PREFERENCES_BROWSER_READY: ready}, stdio: ['ignore', 'pipe', 'pipe'],
    })
  let output = '', fixtureError, base, browser, completed = false
  fixture?.stdout.on('data', value => { output += value })
  fixture?.stderr.on('data', value => { output += value })
  fixture?.on('error', error => { fixtureError = error })
  try {
    const until = Date.now() + 120000
    while (!fs.existsSync(ready)) {
      if (fixtureError) throw fixtureError
      if (fixture && fixture.exitCode !== null) throw Error('Preferences fixture exited: ' + output.slice(-10000))
      if (Date.now() > until) throw Error('Preferences fixture did not start: ' + output.slice(-10000))
      await delay(50)
    }
    base = JSON.parse(fs.readFileSync(ready, 'utf8'))
    assert.equal(base.accounts.length, 2)
    for (const url of [base.main, base.widget].filter(Boolean)) {
      assert(['127.0.0.1', '[::1]', 'localhost'].includes(new URL(url).hostname), 'Only an ephemeral loopback fixture may be tested')
    }
    browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_EXECUTABLE_PATH ? {executablePath: process.env.CHROMIUM_EXECUTABLE_PATH} : {})})
    const errors = []
    async function device(account) {
      const context = await browser.newContext()
      await context.route('**/*', route => {
        assert([base.main, base.widget].includes(new URL(route.request().url()).origin), 'Fixture contacted an external service')
        return route.continue()
      })
      const result = await context.request.post(base.main + '/api/login', {data: {email: account.email, password: account.password}})
      assert.equal(result.status(), 200, 'Synthetic account login failed')
      const page = await context.newPage()
      page.setDefaultTimeout(10000)
      page.on('pageerror', error => errors.push(error.message))
      await page.goto(base.main + '/__preferences_fixture_host')
      await page.evaluate(() => PrismPreferences.ready)
      assert.equal(await page.evaluate(() => PrismPreferences.isReady()), true)
      return {context, page}
    }
    const first = await device(base.accounts[0])
    assert.equal(await first.page.evaluate(() => localStorage.length), 0)
    const saved = await first.page.evaluate(async () => {
      PrismTheme.applyTheme('matrix')
      PrismPreferences.setItem('prism-pinned-notes', JSON.stringify(['fixture-note-é']))
      PrismToolPreferences.set('exec_command', false)
      return PrismPreferences.flush()
    })
    assert.equal(saved, true)

    // A separate browser context has neither this page's memory nor its storage.
    const roaming = await device(base.accounts[0])
    assert.equal(await roaming.page.evaluate(() => PrismTheme.getThemeId()), 'matrix')
    assert.deepEqual(await roaming.page.evaluate(() => JSON.parse(PrismPreferences.getItem('prism-pinned-notes'))), ['fixture-note-é'])
    assert.deepEqual(await roaming.page.evaluate(() => PrismToolPreferences.read().disabled), ['exec_command'])
    assert.equal(await roaming.page.evaluate(() => localStorage.length), 0)

    const other = await device(base.accounts[1])
    assert.equal(await other.page.evaluate(() => PrismTheme.getThemeId()), 'prism-dark')
    assert.equal(await other.page.evaluate(() => PrismPreferences.getItem('prism-pinned-notes')), null)
    assert.deepEqual(await other.page.evaluate(() => PrismToolPreferences.read().disabled), [])
    assert.equal(await other.page.evaluate(async () => { PrismTheme.applyTheme('paper'); return PrismPreferences.flush() }), true)

    assert.equal(await first.page.evaluate(async () => { PrismTheme.applyTheme('nord'); return PrismPreferences.flush() }), true)
    await roaming.page.evaluate(() => PrismPreferences.refresh())
    assert.equal(await roaming.page.evaluate(() => document.documentElement.dataset.theme), 'nord')
    await other.page.evaluate(() => PrismPreferences.refresh())
    assert.equal(await other.page.evaluate(() => PrismTheme.getThemeId()), 'paper')

    if (base.widget) {
      const grant = await roaming.context.request.post(base.main + '/api/widget/grant', {data: {session: 'default'}})
      assert.equal(grant.status(), 200)
      const token = (await grant.json()).token
      const content = await roaming.context.newPage()
      content.on('pageerror', error => errors.push(error.message))
      await content.goto(base.widget + '/widget-frame.html')
      assert.equal(await content.evaluate(async token => (await fetch('/__prism_widget/authorize', {
        method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({token}),
      })).status, token), 200)
      assert.deepEqual(await content.evaluate(() => Promise.all(['/api/preferences', '/preferences.js'].map(async url => (await fetch(url)).status))), [403, 403], 'Authorized widgets must not read account preferences or their helper')
      assert.equal(await content.evaluate(async main => {
        try { await fetch(main + '/api/preferences', {credentials: 'include'}); return false } catch (_) { return true }
      }, base.main), true, 'Generated content must not regain the main preferences API')
      await content.close()
    }

    // Cookies change for the whole browser while an old tab still holds A's
    // preferences. Its queued change must never be written to B's account.
    assert.equal((await first.context.request.post(base.main + '/api/logout')).status(), 200)
    assert.equal((await first.context.request.post(base.main + '/api/login', {data: {email: base.accounts[1].email, password: base.accounts[1].password}})).status(), 200)
    assert.equal(await first.page.evaluate(async () => { PrismTheme.applyTheme('synthwave'); return PrismPreferences.flush() }), false)
    await first.page.getByRole('button', {name: 'Reload', exact: true}).waitFor()
    const b = await first.context.request.get(base.main + '/api/preferences').then(response => response.json())
    assert.equal(b.owner, base.accounts[1].owner)
    assert.equal(b.values['prism-theme'], 'paper', 'A stale tab overwrote the new account')
    const a = await roaming.context.request.get(base.main + '/api/preferences').then(response => response.json())
    assert.equal(a.owner, base.accounts[0].owner)
    assert.equal(a.values['prism-theme'], 'nord')
    assert.deepEqual(errors, [])
    completed = true
    console.log('Actual preferences backend: authenticated devices recover themes, pins and tools; accounts stay isolated; stale tabs cannot write to another account' + (base.widget ? '; generated content cannot read preferences.' : '.'))
  } finally {
    if (browser) await browser.close()
    if (base) fs.writeFileSync(ready + '.stop', '')
    if (fixture) {
      for (let count = 0; fixture.exitCode === null && count < 100; count++) await delay(50)
      if (fixture.exitCode === null) fixture.kill('SIGKILL')
      if (fixture.exitCode !== 0 && base) process.stderr.write(output.slice(-10000))
    }
    fs.rmSync(temporary, {recursive: true, force: true})
    if (completed && fixture && fixture.exitCode !== 0) throw Error('Preferences backend fixture did not finish successfully')
  }
})().catch(error => { console.error(error); process.exitCode = 1 })
