const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const {chromium} = require('playwright')
const root = path.resolve(__dirname, '..'), origin = 'http://fixture.invalid'
const source = fs.readFileSync(path.join(root, 'app.js'), 'utf8')
const rendering = source.slice(source.indexOf('function renderMarkdown('), source.indexOf('// ─── Emoji picker'))
const index = fs.readFileSync(path.join(root, 'index.html'), 'utf8')
const assets = [...index.matchAll(/<(?:script|link)\b[^>]*(?:src|href)="([^\"]+)"[^>]*>(?:<\/script>)?/g)]
  .filter(match => /^\/vendor\/(?:katex|marked|purify|codemirror)/.test(match[1])).map(match => match[0]).join('\n')

;(async () => {
  const executablePath = process.env.CHROMIUM_EXECUTABLE_PATH || process.env.CHROMIUM_EXECUTABLE
  const browser = await chromium.launch({headless: true, ...(executablePath ? {executablePath} : {})})
  try {
    const page = await browser.newPage(), external = [], failures = [], errors = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/*', route => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) {external.push(url.href); return route.abort()}
      if (url.pathname === '/') return route.fulfill({contentType: 'text/html', body: `<!doctype html><html><head>${assets}</head><body><div id="result"></div><div id="editor"></div><script>${rendering}</script></body></html>`})
      const file = path.resolve(root, '.' + url.pathname)
      if (file.startsWith(root + path.sep) && fs.existsSync(file) && fs.statSync(file).isFile()) return route.fulfill({path: file})
      failures.push(url.pathname)
      return route.fulfill({status: 404, body: ''})
    })
    await page.goto(origin)
    await page.evaluate(() => {
      window.originalLibraries = {marked, DOMPurify, katex}
      result.innerHTML = renderMarkdown('# Title\n\n**Strong**\n\n[Docs](https://example.com/docs)\n\n```html\n<img src=x onerror="window.xss=1">\n```\n\n$x^2$\n\n$$x+y$$')
      const modes = ['javascript', 'python', 'go', 'css', 'xml', 'htmlmixed', 'markdown', 'shell', 'yaml', 'toml']
      const editor = CodeMirror(document.getElementById('editor'), {value: 'hello', theme: 'dracula'})
      for (const mode of modes) {
        if (!CodeMirror.modes[mode]) throw new Error('Missing editor mode: ' + mode)
        editor.setOption('mode', mode)
        if (editor.getMode().name === 'null') throw new Error('Editor mode did not initialize: ' + mode)
      }
      return document.fonts.load('16px KaTeX_Main')
    })
    assert.equal(await page.locator('#result h1').textContent(), 'Title')
    assert.equal(await page.locator('#result strong').textContent(), 'Strong')
    assert.equal(await page.locator('#result a').getAttribute('href'), 'https://example.com/docs')
    assert.match(await page.locator('#result pre code').textContent(), /<img src=x/)
    assert.equal(await page.locator('#result img').count(), 0)
    assert.equal(await page.locator('#result .katex').count(), 2)
    assert.equal(await page.locator('.CodeMirror').count(), 1)

    const unsafe = '<img src=x onerror="window.xss=1"><script>window.xss=1</script><a href="javascript:window.xss=1">link</a>'
    await page.evaluate(text => {result.innerHTML = renderMarkdown(text)}, unsafe)
    assert.equal(await page.locator('#result script, #result [onerror], #result [onload], #result a[href^="javascript:"]').count(), 0)
    assert.equal(await page.evaluate(() => window.xss), undefined)

    for (const missing of ['marked', 'DOMPurify']) {
      await page.evaluate(({missing, text}) => {
        Object.assign(window, originalLibraries)
        window[missing] = undefined
        result.innerHTML = renderMarkdown(text)
      }, {missing, text: unsafe})
      assert.equal(await page.locator('#result').textContent(), unsafe)
      assert.equal(await page.locator('#result img, #result script, #result a').count(), 0)
    }
    await page.evaluate(() => {
      Object.assign(window, originalLibraries)
      window.katex = undefined
      result.innerHTML = renderMarkdown('$<img src=x onerror="window.xss=1">$\n\n$$<script>window.xss=1</script>$$')
    })
    assert.match(await page.locator('#result').textContent(), /\$<img/)
    assert.equal(await page.locator('#result img, #result script').count(), 0)
    assert.equal(await page.evaluate(() => window.xss), undefined)
    assert.deepEqual(external, [], 'rendering and editing requested an external dependency')
    assert.deepEqual(failures.filter(url => url !== '/x'), [], 'a local dependency or font was missing')
    assert.deepEqual(errors, [])
    console.log('markdown rendering, XSS fallback, local fonts and all ten editor modes passed')
  } finally {
    await browser.close()
  }
})().catch(error => {console.error(error); process.exitCode = 1})
