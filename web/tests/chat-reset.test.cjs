const {test} = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm')
const source = fs.readFileSync(path.join(__dirname, '../app.js'), 'utf8')
const start = source.indexOf('window.resetChat =')
const end = source.indexOf('\n}', start) + 2

function fixture() {
  let resolve
  const sent = []
  const ctx = {window: {}, currentSessionID: 'u1-main', ws: {}, pendingImages: ['image'], pendingFiles: ['file'], pendingAttachments: ['attachment'],
    prismConfirm: () => new Promise(r => {resolve = r}), send: m => sent.push(m), renderPreviews() {}}
  vm.createContext(ctx)
  vm.runInContext(source.slice(start, end), ctx)
  return {ctx, sent, answer: value => resolve(value)}
}
test('clearing a chat requires confirmation and preserves drafts on cancel', async () => {
  const f = fixture(), pending = f.ctx.window.resetChat()
  assert.equal(f.sent.length, 0)
  f.answer(false); await pending
  assert.equal(f.sent.length, 0)
  assert.equal(f.ctx.pendingAttachments.length, 1)
})
test('confirmation clears the selected chat', async () => {
  const f = fixture(), pending = f.ctx.window.resetChat()
  f.answer(true); await pending
  assert.equal(f.sent.length, 1)
  assert.equal(f.sent[0].type, 'reset_chat')
  assert.equal(f.ctx.pendingAttachments.length, 0)
})
for (const change of ['workspace', 'socket']) {
  test(`a ${change} change while confirming cannot clear another conversation`, async () => {
    const f = fixture(), pending = f.ctx.window.resetChat()
    if (change === 'workspace') f.ctx.currentSessionID = 'u1-other'
    else f.ctx.ws = {}
    f.answer(true); await pending
    assert.equal(f.sent.length, 0)
    assert.equal(f.ctx.pendingAttachments.length, 1)
  })
}
