const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const {chromium} = require('playwright')
const root = path.resolve(__dirname, '..'), origin = 'http://fixture.invalid'
const source = fs.readFileSync(path.join(root, 'app.js'), 'utf8')
const editor = source.slice(source.indexOf('// ─── File editor'), source.indexOf('// Listen for postMessage calls'))
const index = fs.readFileSync(path.join(root, 'index.html'), 'utf8')
const markup = index.slice(index.indexOf('<!-- File editor panel -->'), index.indexOf('<!-- Secret input dialog -->'))
const deferred = () => {let resolve; const promise = new Promise(r => {resolve = r}); return {promise, resolve}}

;(async () => {
  const executablePath = process.env.CHROMIUM_EXECUTABLE_PATH || process.env.CHROMIUM_EXECUTABLE
  const browser = await chromium.launch({headless: true, ...(executablePath ? {executablePath} : {})})
  try {
    async function fixture({gate, fail} = {}) {
      const page = await browser.newPage(), requested = [], errors = [], failures = []
      let failed = false
      page.on('pageerror', e => errors.push(e.message))
      await page.route('**/*', async route => {
        const url = new URL(route.request().url())
        assert.equal(url.origin, origin, 'file editor requested an external resource')
        if (url.pathname === '/') return route.fulfill({contentType: 'text/html', body: `<!doctype html><html><head><link rel="stylesheet" href="/style.css"><script src="/editor-loader.js"></script></head><body>${markup}<script>window.sent=[];function send(message){sent.push(message)};${editor}</script></body></html>`})
        requested.push(url.pathname)
        if (gate && url.pathname === '/vendor/codemirror/codemirror.js') await gate.promise
        if (url.pathname === fail && !failed) {failed = true; return route.abort()}
        const file = path.resolve(root, '.' + url.pathname)
        if (file.startsWith(root + path.sep) && fs.existsSync(file) && fs.statSync(file).isFile()) return route.fulfill({path: file})
        failures.push(url.pathname); return route.fulfill({status: 404, body: ''})
      })
      await page.goto(origin)
      assert.equal(requested.some(p => p.startsWith('/vendor/codemirror/')), false, 'editor dependencies loaded during startup')
      return {page, requested, errors, failures}
    }

    const gate = deferred(), f = await fixture({gate})
    await f.page.evaluate(() => openEditor('first.py', 'initial'))
    await f.page.locator('#editor-plain').fill('edited during loading')
    await f.page.locator('#editor-plain').press('Control+s')
    assert.deepEqual(await f.page.evaluate(() => sent), [{type: 'file_save', path: 'first.py', data: {content: 'edited during loading'}}])
    await f.page.evaluate(() => openEditor('second.go', 'second'))
    await f.page.locator('#editor-plain').fill('latest draft')
    gate.resolve()
    await f.page.locator('.CodeMirror').waitFor({state: 'visible'})
    assert.equal(await f.page.evaluate(() => cmEditor.getValue()), 'latest draft')
    assert.equal(await f.page.evaluate(() => cmEditor.getMode().name), 'go')
    await f.page.locator('.CodeMirror textarea').press('Control+s')
    assert.equal((await f.page.evaluate(() => sent)).at(-1).data.content, 'latest draft')
    assert.equal(f.requested.filter(p => p.startsWith('/vendor/codemirror/')).length, 13)
    await f.page.evaluate(() => {closeEditor(); openEditor('third.toml', 'third')})
    assert.equal(await f.page.evaluate(() => cmEditor.getMode().name), 'toml')
    assert.equal(f.requested.filter(p => p.startsWith('/vendor/codemirror/')).length, 13, 'reopen loaded duplicate assets')
    assert.deepEqual(f.errors, []); assert.deepEqual(f.failures, [])
    await f.page.close()

    const closedGate = deferred(), closed = await fixture({gate: closedGate})
    await closed.page.evaluate(() => {openEditor('closed.md', 'closed'); closeEditor()})
    closedGate.resolve()
    await closed.page.waitForFunction(() => typeof CodeMirror === 'function' && CodeMirror.modes.markdown)
    assert.equal(await closed.page.locator('.CodeMirror').count(), 0, 'late load reopened closed file')
    assert.equal(await closed.page.locator('#editor-panel').getAttribute('class'), '')
    await closed.page.close()

    const failPath = '/vendor/codemirror/mode/python/python.js', offline = await fixture({fail: failPath})
    await offline.page.evaluate(() => openEditor('retry.py', 'keep'))
    await offline.page.locator('#editor-retry-btn').waitFor({state: 'visible'})
    await offline.page.locator('#editor-plain').fill('retained after failure')
    await offline.page.locator('#editor-plain').press('Control+s')
    assert.equal((await offline.page.evaluate(() => sent)).at(-1).data.content, 'retained after failure')
    await offline.page.locator('#editor-retry-btn').click()
    await offline.page.locator('.CodeMirror').waitFor({state: 'visible'})
    assert.equal(await offline.page.evaluate(() => cmEditor.getValue()), 'retained after failure')
    assert.equal(await offline.page.evaluate(() => cmEditor.getMode().name), 'python')
    assert.equal(offline.requested.filter(p => p === failPath).length, 2)
    assert.equal(offline.requested.filter(p => p === '/vendor/codemirror/codemirror.js').length, 1)
    assert.deepEqual(offline.errors, []); assert.deepEqual(offline.failures, [])
    await offline.page.close()
    console.log('lazy local editor, pending edits/save, latest file, close/reopen, failed resource retry and language modes passed')
  } finally {await browser.close()}
})().catch(error => {console.error(error); process.exitCode = 1})
