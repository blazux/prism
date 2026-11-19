const {test} = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm'), crypto = require('node:crypto')
const root = path.resolve(__dirname, '..')
const source = fs.readFileSync(path.join(root, 'app.js'), 'utf8')
const rendering = source.slice(source.indexOf('function renderMarkdown('), source.indexOf('// ─── Emoji picker'))

function fixture() {
  const context = {window: null}
  context.window = context
  vm.createContext(context)
  vm.runInContext(fs.readFileSync(path.join(root, 'vendor/marked/marked.min.js'), 'utf8'), context)
  context.DOMPurify = {sanitize: html => html}
  vm.runInContext(rendering, context)
  return context
}

const unsafe = '**Result**\n<img src=x onerror="alert(1)">\n<script>alert(1)</script>\n[open](javascript:alert(1)) & text'
for (const missing of ['marked', 'DOMPurify']) {
  test(`missing ${missing} keeps the complete response as inert text`, () => {
    const context = fixture()
    context[missing] = undefined
    const html = context.renderMarkdown(unsafe)
    assert.equal(html, context.escHtml(unsafe).replace(/\n/g, '<br>'))
    assert.doesNotMatch(html, /<(?:img|script|a)\b/i)
  })
}

test('unsupported and failed parsing or sanitizing fail closed', () => {
  for (const failure of ['unsupported', 'parse', 'sanitize']) {
    const context = fixture()
    if (failure === 'unsupported') context.DOMPurify.isSupported = false
    else if (failure === 'parse') context.marked.parse = () => {throw new Error('parser failed')}
    else context.DOMPurify.sanitize = () => {throw new Error('sanitizer failed')}
    assert.equal(context.renderMarkdown(unsafe), context.escHtml(unsafe).replace(/\n/g, '<br>'))
  }
})

test('only the sanitizer result is inserted after markdown parsing', () => {
  const context = fixture(), seen = []
  context.DOMPurify.sanitize = html => {seen.push(html); return '<p>Sanitized result</p>'}
  assert.equal(context.renderMarkdown(unsafe), '<p>Sanitized result</p>')
  assert.match(seen[0], /<strong>Result<\/strong>/)
  assert.match(seen[0], /<img src=x onerror=/)
})

test('normal markdown and fenced artifacts still use the pinned parser', () => {
  const context = fixture()
  const html = context.renderMarkdown('# Title\n\n**Strong**\n\n```html\n<img src=x onerror="alert(1)">\n```')
  assert.match(html, /<h1>Title<\/h1>/)
  assert.match(html, /<strong>Strong<\/strong>/)
  assert.match(html, /<pre><code class="language-html">&lt;img/)
  assert.doesNotMatch(html, /<img\b/)
  context.marked = undefined
  assert.equal(context.renderMarkdown('```html\n<img src=x>\n```'), '```html<br>&lt;img src=x&gt;<br>```')
})

test('missing or failed KaTeX preserves inert inline and display formulas', () => {
  const text = '$<img src=x onerror="alert(1)">$\n\n$$<script>alert(1)</script>$$'
  for (const throws of [false, true]) {
    const context = fixture()
    if (throws) context.katex = {renderToString() {throw new Error('math failed')}}
    const html = context.renderMarkdown(text)
    assert.match(html, /\$&lt;img/)
    assert.match(html, /\$\$&lt;script&gt;/)
    assert.doesNotMatch(html, /<(?:img|script)\b/i)
  }
})

test('thinking and streaming indicators remain available without dependencies', () => {
  const context = fixture()
  context.marked = undefined
  assert.match(context.renderMarkdown('<think><img src=x></think>Reply'), /thinking-content">&lt;img src=x&gt;<\/div>/)
  assert.match(context.renderMarkdown('<think>Still thinking'), /thinking-indicator/)
  assert.match(context.renderMarkdown('\uE000MATH999\uE000'), /MATH999/)
})

test('dashboard dependencies and every KaTeX font are local verified distribution files', () => {
  const html = fs.readFileSync(path.join(root, 'index.html'), 'utf8')
  const refs = [...html.matchAll(/<(?:script|link)\b[^>]*(?:src|href)="([^"]+)"/g)].map(match => match[1])
  for (const required of ['/vendor/marked/marked.min.js', '/vendor/purify.min.js', '/vendor/katex/katex.min.js', '/editor-loader.js']) {
    assert.ok(refs.includes(required), `missing dashboard dependency: ${required}`)
  }
  for (const ref of refs) {
    assert.ok(ref.startsWith('/'), `external dashboard dependency: ${ref}`)
    assert.ok(fs.statSync(path.join(root, ref)).isFile(), `missing dependency: ${ref}`)
  }
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'vendor/ui-dependencies.json'), 'utf8'))
  assert.deepEqual(manifest.packages.map(p => [p.name, p.version]), [['marked', '9.1.6'], ['katex', '0.16.47'], ['codemirror', '5.65.16']])
  const assets = new Set()
  for (const pkg of manifest.packages) for (const asset of pkg.assets) {
    const bytes = fs.readFileSync(path.join(root, 'vendor', asset.path))
    assert.equal(bytes.length, asset.bytes, asset.path)
    assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'), asset.sha256, asset.path)
    assets.add(asset.path)
  }
  const css = fs.readFileSync(path.join(root, 'vendor/katex/katex.min.css'), 'utf8')
  const fonts = [...css.matchAll(/url\(([^)]+)\)/g)].map(match => match[1])
  assert.equal(fonts.length, 60)
  for (const font of fonts) assert.ok(assets.has('katex/' + font), `missing font: ${font}`)
})
