const {test} = require('node:test'), assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm')
const root = path.resolve(__dirname, '..')
const app = fs.readFileSync(path.join(root, 'app.js'), 'utf8')
const editor = app.slice(app.indexOf('// ─── File editor'), app.indexOf('// Listen for postMessage calls'))
const tick = () => new Promise(resolve => setImmediate(resolve))
const deferred = () => {let resolve, reject; const promise = new Promise((a, b) => {resolve = a; reject = b}); return {promise, resolve, reject}}

function fixture() {
  const fields = {}, sent = [], timers = [], instances = []
  const document = {activeElement: null, getElementById(id) {
    return fields[id] ||= {style: {}, textContent: '', value: '', selectionStart: 0, selectionEnd: 0, scrollTop: 0, scrollLeft: 0,
      classList: {add() {}, remove() {}}, focus() {document.activeElement = this},
      setRangeText(text, start, end) {this.value = this.value.slice(0, start) + text + this.value.slice(end); this.selectionStart = this.selectionEnd = start + text.length}}
  }}
  const loading = deferred()
  const ctx = {window: null, document, send: m => sent.push(m), setTimeout: fn => timers.push(fn), PrismCodeEditor: {load: () => loading.promise},
    CodeMirror: (_, options) => {
      const cm = {options, value: '', setOption(k, v) {this.options[k] = v}, setValue(v) {this.value = v}, getValue() {return this.value},
        clearHistory() {}, refresh() {}, focus() {}, posFromIndex: i => i, setSelection(a, b) {this.selection = [a, b]}, scrollTo() {}}
      instances.push(cm); return cm
    }}
  ctx.window = ctx
  vm.createContext(ctx); vm.runInContext(editor, ctx)
  return {ctx, document, fields, sent, timers, instances, loading}
}

test('file editing and saving remain available while syntax highlighting loads', async () => {
  const f = fixture(); f.ctx.openEditor('draft.py', 'original')
  const plain = f.fields['editor-plain']; plain.value = 'my edits'; plain.selectionStart = 1; plain.selectionEnd = 5
  let prevented = false
  f.ctx.editorPlainKey({ctrlKey: true, key: 's', preventDefault() {prevented = true}})
  assert.equal(prevented, true); assert.equal(f.sent[0].path, 'draft.py'); assert.equal(f.sent[0].data.content, 'my edits')
  f.loading.resolve(); await tick()
  assert.equal(f.instances.length, 1); assert.equal(f.instances[0].value, 'my edits')
  assert.deepEqual(f.instances[0].selection, [1, 5]); assert.equal(f.instances[0].options.mode, 'python')
  assert.equal(plain.style.display, 'none')
  f.instances[0].options.extraKeys['Ctrl-S']()
  assert.equal(f.sent[1].data.content, 'my edits')
})

test('the latest file wins during loading, and closing prevents a late editor from opening', async () => {
  const f = fixture(); f.ctx.openEditor('first.go', 'first'); f.ctx.openEditor('second.md', 'second')
  f.loading.resolve(); await tick()
  assert.equal(f.instances.length, 1); assert.equal(f.instances[0].value, 'second'); assert.equal(f.instances[0].options.mode, 'markdown')
  const closed = fixture(); closed.ctx.openEditor('closed.txt', 'closed'); closed.ctx.closeEditor(); closed.loading.resolve(); await tick()
  assert.equal(closed.instances.length, 0)
  closed.ctx.openEditor('reopened.toml', 'reopened'); await tick()
  assert.equal(closed.instances[0].value, 'reopened'); assert.equal(closed.instances[0].options.mode, 'toml')
})

test('a failed dependency keeps the draft editable and retry preserves newer changes', async () => {
  const f = fixture(); f.ctx.openEditor('file.json', '{}'); f.loading.reject(new Error('offline')); await tick()
  assert.match(f.fields['editor-status'].textContent, /unavailable/)
  assert.equal(f.fields['editor-plain'].style.display, '')
  f.fields['editor-plain'].value = '{"kept":true}'
  f.ctx.editorSave(); assert.equal(f.sent[0].data.content, '{"kept":true}')
  f.ctx.PrismCodeEditor.load = () => Promise.resolve(); f.ctx.retryEditor(); await tick()
  assert.equal(f.instances[0].value, '{"kept":true}'); assert.equal(f.instances[0].options.mode.json, true)
  assert.equal(f.fields['editor-retry-btn'].style.display, 'none')
})

test('the plain editor handles indentation and Escape without closing another panel', () => {
  const f = fixture(); f.ctx.openEditor('file', 'ab'); const plain = f.fields['editor-plain']
  plain.selectionStart = plain.selectionEnd = 1
  f.ctx.editorPlainKey({key: 'Tab', target: plain, preventDefault() {}})
  assert.equal(plain.value, 'a  b')
  let stopped = false
  f.ctx.editorPlainKey({key: 'Escape', preventDefault() {}, stopPropagation() {stopped = true}})
  assert.equal(stopped, true); f.ctx.editorSave(); assert.equal(f.sent.length, 0)
})

test('editor dependencies are lazy, shared by concurrent opens, and failed assets can retry', async () => {
  const index = fs.readFileSync(path.join(root, 'index.html'), 'utf8')
  assert.doesNotMatch(index, /(?:src|href)="\/vendor\/codemirror\//)
  assert.match(index, /src="\/editor-loader.js"/)
  const requested = [], failed = '/vendor/codemirror/mode/go/go.js'
  let fail = true
  const ctx = {window: null, document: {createElement: () => ({remove() {}}), head: {appendChild(el) {
    const url = el.src || el.href; requested.push(url)
    queueMicrotask(() => {if (url === failed && fail) {fail = false; el.onerror()} else {if (url.endsWith('/codemirror.js')) ctx.CodeMirror = function () {}; el.onload()}})
  }}}}
  ctx.window = ctx; vm.createContext(ctx); vm.runInContext(fs.readFileSync(path.join(root, 'editor-loader.js'), 'utf8'), ctx)
  assert.equal(requested.length, 0)
  const first = ctx.PrismCodeEditor.load(); assert.equal(ctx.PrismCodeEditor.load(), first)
  await assert.rejects(first, /Could not load/); await tick()
  await ctx.PrismCodeEditor.load()
  assert.equal(requested.filter(p => p.endsWith('/codemirror.js')).length, 1)
  assert.equal(requested.filter(p => p === failed).length, 2)
  assert.equal(new Set(requested).size, 13)
  assert.ok(requested.every(p => p.startsWith('/vendor/codemirror/')))
})
