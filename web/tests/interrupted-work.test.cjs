const {test} = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm')
const source = fs.readFileSync(path.join(__dirname, '../app.js'), 'utf8')
function fn(name) {
  const start = source.indexOf('function ' + name + '(')
  assert.ok(start >= 0, name)
  return source.slice(start, source.indexOf('\n}', start) + 2)
}
function fixture() {
  const sent = [], messages = [], notices = []
  const input = {value: 'My next request', readOnly: false}
  const status = {children: [], set textContent(value) {this.text = value; this.children = []}, append(...nodes) {this.children.push(...nodes)}}
  const context = vm.createContext({
    PrismPreferences: {isReady: () => true},
    currentSessionID: 'main', activeRunStatus: 'interrupted', isStreaming: false,
    pendingChats: new Map(), pendingImages: [{data: 'image', name: 'keep.png'}], pendingFiles: [{name: 'keep.txt', path: 'keep.txt'}],
    ws: {readyState: 1, send: raw => sent.push(JSON.parse(raw))}, WebSocket: {OPEN: 1},
    crypto: require('node:crypto').webcrypto, document: {
      getElementById: id => id === 'chat-input' ? input : id === 'chat-delivery-status' ? status : null,
      createElement: () => ({})
    },
    getDisabledTools: () => [], renderPreviews() {}, autoResizeTextarea() {},
    appendUserMessage: (...args) => messages.push(args), appendError() {},
    showToast: item => notices.push(item)
  })
  vm.runInContext(['send', 'sendChat', 'replyToWidgetChat', 'receiveChatReceipt', 'renderChatStatus', 'resumeInterruptedChat'].map(fn).join('\n'), context)
  return {context, sent, messages, notices, input, status}
}

test('Resume is offered only for an interrupted, connected and idle conversation', () => {
  const f = fixture()
  f.context.renderChatStatus()
  assert.equal(f.status.children.at(-1).textContent, 'Resume')
  assert.equal(f.sent.length, 0, 'displaying the control must not resume automatically')
  for (const state of ['idle', 'running', 'waiting_for_input', 'waiting_for_approval', 'failed', 'completed', 'stopped', 'timed_out']) {
    f.context.activeRunStatus = state; f.context.renderChatStatus()
    assert.equal(f.status.children.some(node => node?.textContent === 'Resume'), false, state)
  }
  f.context.activeRunStatus = 'interrupted'; f.context.ws.readyState = 3; f.context.renderChatStatus()
  assert.equal(f.status.children.length, 0)
  f.context.resumeInterruptedChat()
  assert.equal(f.sent.length, 0)
})

test('Resume sends one ordinary follow-up and preserves the separate draft and attachments', () => {
  const f = fixture()
  f.context.renderChatStatus(); f.status.children.at(-1).onclick()
  f.context.resumeInterruptedChat()
  assert.equal(f.sent.length, 1)
  assert.equal(f.sent[0].type, 'chat')
  assert.equal(f.sent[0].content, 'Continue.')
  assert.ok(f.sent[0].requestId)
  assert.equal(f.sent[0].images, undefined)
  assert.equal(f.sent[0].files, undefined)
  assert.equal(f.input.value, 'My next request')
  assert.equal(f.input.readOnly, false)
  f.context.receiveChatReceipt({requestId: f.sent[0].requestId, status: 'accepted'})
  assert.equal(f.messages.length, 1)
  assert.equal(f.messages[0][0], 'Continue.')
  assert.equal(f.input.value, 'My next request')
  assert.equal(f.context.pendingImages.length, 1)
  assert.equal(f.context.pendingFiles.length, 1)
})

test('an uncertain resume keeps its identifier and does not retry on its own', () => {
  const f = fixture(); f.context.resumeInterruptedChat()
  const id = f.sent[0].requestId
  f.context.receiveChatReceipt({requestId: id, status: 'unknown'})
  f.context.resumeInterruptedChat()
  assert.equal(f.sent.length, 1)
  const retry = f.status.children.at(-1)
  assert.equal(retry.textContent, 'Retry send')
  retry.onclick()
  assert.equal(f.sent.length, 2)
  assert.equal(f.sent[1].requestId, id)
  assert.equal(f.input.value, 'My next request')
})

test('resume acceptance does not clear a draft which happens to say Continue', () => {
  const f = fixture(); f.input.value = 'Continue.'
  f.context.resumeInterruptedChat()
  f.context.receiveChatReceipt({requestId: f.sent[0].requestId, status: 'accepted'})
  assert.equal(f.input.value, 'Continue.')
})
