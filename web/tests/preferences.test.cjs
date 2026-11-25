const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const test = require('node:test')
const vm = require('node:vm')

const source = fs.readFileSync(path.join(__dirname, '../preferences.js'), 'utf8')
const tick = () => new Promise(resolve => setImmediate(resolve))

function fixture(legacy = {}) {
  const requests = [], events = [], listeners = new Map(), timers = new Map()
  let timerID = 0
  const context = {
    console,
    TextEncoder,
    AbortSignal: { timeout: () => ({}) },
    CustomEvent: class { constructor(type, options) { this.type = type; this.detail = options.detail } },
    BroadcastChannel: class { postMessage() {} },
    localStorage: {
      getItem: key => legacy[key] ?? null,
      setItem() { throw Error('Browser storage must never be written') },
      removeItem() { throw Error('Browser storage must never be changed') },
    },
    document: { body: null, hidden: false, addEventListener() {} },
    location: { reload() {} },
    dispatchEvent: event => events.push(event),
    addEventListener: (name, callback) => listeners.set(name, callback),
    setTimeout: callback => { timers.set(++timerID, callback); return timerID },
    clearTimeout: id => timers.delete(id),
    fetch: (url, options) => new Promise((resolve, reject) => requests.push({
      url, method: options.method, body: options.body ? JSON.parse(options.body) : null, keepalive: options.keepalive,
      resolve, reject,
    })),
  }
  context.window = context
  vm.runInNewContext(source, context)
  return {
    preferences: context.PrismPreferences, requests, events,
    async reply(index, values, {owner = 'u42', legacyImportAllowed = false, status = 200} = {}) {
      requests[index].resolve({status, ok: status >= 200 && status < 300,
        json: async () => ({values, owner, legacyImportAllowed})})
      await tick()
    },
    async fail(index) { requests[index].reject(Error('Network unavailable')); await tick() },
  }
}

test('a human account imports only missing legacy values after explicit consent', async () => {
  const f = fixture({'prism-theme': 'legacy-theme', 'prism-cal-mode': 'week'})
  await f.reply(0, {'prism-theme': 'server-theme'})
  await f.preferences.ready
  assert.equal(f.requests.length, 1, 'shared-browser preferences must not import automatically')
  assert.equal(f.preferences.getItem('prism-theme'), 'server-theme')
  const imported = f.preferences.importLegacy()
  await tick()
  assert.deepEqual(JSON.parse(JSON.stringify(f.requests[1].body)), {
    owner: 'u42', values: {'prism-cal-mode': 'week'},
  })
  await f.reply(1, {'prism-theme': 'server-theme', 'prism-cal-mode': 'week'})
  await imported
  assert.equal(f.preferences.getItem('prism-theme'), 'server-theme')
})

test('offline writes remain on the page and can be saved without losing edits', async () => {
  const f = fixture()
  await f.reply(0, {'prism-theme': 'original'})
  await f.preferences.ready
  f.preferences.setItem('prism-theme', 'edited')
  const failed = f.preferences.flush()
  await f.fail(1)
  assert.equal(await failed, false)
  assert.equal(f.preferences.getItem('prism-theme'), 'edited')
  const retry = f.preferences.flush()
  assert.deepEqual(JSON.parse(JSON.stringify(f.requests[2].body)), {
    owner: 'u42', values: {'prism-theme': 'edited'},
  })
  await f.reply(2, {'prism-theme': 'edited'})
  assert.equal(await retry, true)
  assert.equal(await f.preferences.flush(), true)
  assert.equal(f.requests.length, 3)
})

test('an account switch stops writes from the old page', async () => {
  const f = fixture()
  await f.reply(0, {'prism-theme': 'account-A'})
  await f.preferences.ready
  const switched = f.preferences.refresh()
  const rejected = assert.rejects(switched)
  await f.reply(1, {'prism-theme': 'account-B'}, {owner: 'u99'})
  await rejected
  f.preferences.setItem('prism-theme', 'must-not-save')
  await f.preferences.flush()
  assert.equal(f.preferences.getItem('prism-theme'), 'account-A')
  assert.equal(f.requests.length, 2)
})

test('edits made while an earlier save is in flight are saved afterwards', async () => {
  const f = fixture()
  await f.reply(0, {'prism-theme': 'original'})
  await f.preferences.ready
  f.preferences.setItem('prism-theme', 'first')
  const saved = f.preferences.flush()
  const concurrentSave = f.preferences.flush()
  f.preferences.setItem('prism-theme', 'newest')
  await f.reply(1, {'prism-theme': 'first'})
  assert.equal(f.requests[2].body.values['prism-theme'], 'newest')
  await f.reply(2, {'prism-theme': 'newest'})
  assert.equal(await saved, true)
  assert.equal(await concurrentSave, true)
  assert.equal(f.requests.length, 3, 'concurrent flushes must not duplicate the writes')
  assert.equal(f.preferences.getItem('prism-theme'), 'newest')
})

test('an unknown account after a failed initial load remains unready until a successful retry', async () => {
  const f = fixture({'prism-theme': 'another-persons-browser-value'})
  await f.fail(0)
  await f.preferences.ready
  assert.equal(f.preferences.isReady(), false)
  assert.equal(f.preferences.getItem('prism-theme'), null)
  const reloaded = f.preferences.ensureReady()
  await f.reply(1, {'prism-approval': 'manual', 'prism-theme': 'account-theme'})
  assert.equal(await reloaded, true)
  assert.equal(f.preferences.getItem('prism-approval'), 'manual')
  assert.equal(f.preferences.getItem('prism-theme'), 'account-theme')
  assert.equal(f.requests.length, 2, 'unknown browser ownership must not trigger an import')
})

test('an old refresh cannot undo a confirmed newer save', async () => {
  const f = fixture()
  await f.reply(0, {'prism-theme': 'original'})
  await f.preferences.ready
  const oldRefresh = f.preferences.refresh()
  f.preferences.setItem('prism-theme', 'newest')
  const saved = f.preferences.flush()
  await f.reply(2, {'prism-theme': 'newest'})
  assert.equal(await saved, true)
  await f.reply(1, {'prism-theme': 'original'})
  // A client may discard the old response or issue a fresh GET; either must
  // preserve the confirmed write rather than restoring the stale snapshot.
  for (let index = 3; index < f.requests.length; index++) {
    assert.equal(f.requests[index].method, 'GET')
    await f.reply(index, {'prism-theme': 'newest'})
  }
  await oldRefresh
  assert.equal(f.preferences.getItem('prism-theme'), 'newest')
})

test('an optional legacy import failure preserves known server preferences', async () => {
  const f = fixture({'prism-cal-mode': 'week'})
  await f.reply(0, {'prism-approval': 'manual'}, {owner: 'mono', legacyImportAllowed: true})
  assert.equal(f.requests[1].method, 'POST')
  await f.fail(1)
  await f.preferences.ready
  assert.equal(f.preferences.getItem('prism-approval'), 'manual')
})

test('a failed import cannot silently enable tools previously disabled in the mono browser', async () => {
  const f = fixture({'agent-config': JSON.stringify({disabled: ['exec_command'], obsoleteModelChoice: 'old'}), 'prism-approval': 'manual'})
  await f.reply(0, {}, {owner: 'mono', legacyImportAllowed: true})
  assert.deepEqual(JSON.parse(f.requests[1].body.values['agent-config']), {disabled: ['exec_command']})
  await f.fail(1)
  await f.preferences.ready
  assert.equal(f.preferences.isReady(), false)
  const retried = f.preferences.ensureReady()
  await f.reply(2, {}, {owner: 'mono', legacyImportAllowed: true})
  await f.reply(3, {'agent-config': JSON.stringify({disabled: ['exec_command']}), 'prism-approval': 'manual'}, {owner: 'mono', legacyImportAllowed: true})
  assert.equal(await retried, true)
  assert.equal(f.preferences.getItem('prism-approval'), 'manual')
  assert.deepEqual(JSON.parse(f.preferences.getItem('agent-config')).disabled, ['exec_command'])
})

test('valid large Unicode preferences are saved without exceeding the browser keepalive byte limit', async () => {
  const f = fixture()
  await f.reply(0, {})
  await f.preferences.ready
  const pins = JSON.stringify(Array.from({length: 160}, (_, i) => String(i) + '界'.repeat(150)))
  f.preferences.setItem('prism-pinned-notes', pins)
  const saved = f.preferences.flush()
  assert.equal(f.requests[1].keepalive, false)
  await f.reply(1, {'prism-pinned-notes': pins})
  assert.equal(await saved, true)
  assert.equal(f.preferences.getItem('prism-pinned-notes'), pins)
})

test('a collection cannot be replaced with an empty local snapshot before it has loaded', async () => {
  const f = fixture()
  assert.equal(f.preferences.setItem('agent-config', JSON.stringify({disabled: []})), false)
  assert.equal(f.preferences.setItem('prism-pinned-notes', '[]'), false)
  await f.reply(0, {'agent-config': JSON.stringify({disabled: ['exec_command']}), 'prism-pinned-notes': '["kept"]'})
  await f.preferences.ready
  assert.deepEqual(JSON.parse(f.preferences.getItem('agent-config')).disabled, ['exec_command'])
  assert.equal(f.preferences.getItem('prism-pinned-notes'), '["kept"]')
  assert.equal(await f.preferences.flush(), true)
  assert.equal(f.requests.length, 1)
})

test('unrelated legacy appearance errors do not block successfully imported tool restrictions', async () => {
  const f = fixture({'prism-theme': 'invalid old theme', 'agent-config': '{"disabled":["exec_command"]}', 'prism-approval': 'manual'})
  await f.reply(0, {}, {owner: 'mono', legacyImportAllowed: true})
  assert.equal(f.requests[1].body.values['prism-theme'], undefined)
  await f.reply(1, {'agent-config': '{"disabled":["exec_command"]}', 'prism-approval': 'manual'}, {owner: 'mono', legacyImportAllowed: true})
  await f.reply(2, {}, {owner: 'mono', legacyImportAllowed: true, status: 400})
  await f.preferences.ready
  assert.equal(f.preferences.isReady(), true)
  assert.equal(f.preferences.getItem('prism-approval'), 'manual')
  assert.deepEqual(JSON.parse(f.preferences.getItem('agent-config')).disabled, ['exec_command'])
})
