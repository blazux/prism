const {test} = require('node:test')
const assert = require('node:assert/strict')
const vm = require('node:vm')
const fs = require('node:fs')
const path = require('node:path')
const source = fs.readFileSync(path.join(__dirname, '../app.js'), 'utf8')
const identity = source.slice(source.indexOf('async function loadIdentity()'), source.indexOf('// Show the per-widget share'))

test('identity reads start together and a failed endpoint preserves the other settings', async () => {
  const pending = new Map()
  let refreshed = 0
  const context = vm.createContext({
    ME: {uid: '', name: 'You', isAdmin: false, terminalAvailable: true},
    WIDGET_HOSTING: null, AGENT_NAME: 'Agent', DISABLED_APPS: new Set(), MY_GROUPS: [],
    fetch: url => new Promise((resolve, reject) => pending.set(url, {resolve, reject})),
    refreshShareAffordances() { refreshed++ }
  })
  vm.runInContext(identity, context)
  const loaded = context.loadIdentity()
  assert.deepEqual([...pending.keys()].sort(), ['/api/agent/name', '/api/me', '/api/my/groups', '/api/platform', '/api/profile'])
  const reply = (url, value) => pending.get(url).resolve({json: async () => value})
  reply('/api/me', {isAdmin: true, terminalAvailable: false, widgetHosting: {grantURL: '/grant'}})
  pending.get('/api/platform').reject(new Error('offline'))
  reply('/api/my/groups', {groups: [{id: 4, name: 'Admins'}, {groupId: 5, groupName: 'Members'}]})
  reply('/api/agent/name', {name: 'Kate'})
  await new Promise(setImmediate)
  assert.equal(refreshed, 0)
  reply('/api/profile', {userId: 7, displayName: 'Vincent'})
  await loaded
  assert.equal(refreshed, 1)
  assert.equal(context.ME.uid, 7)
  assert.equal(context.ME.name, 'Vincent')
  assert.equal(context.ME.isAdmin, true)
  assert.equal(context.ME.terminalAvailable, false)
  assert.equal(context.AGENT_NAME, 'Kate')
  assert.equal(context.WIDGET_HOSTING.grantURL, '/grant')
  assert.equal(context.DISABLED_APPS.size, 0)
  assert.deepEqual(JSON.parse(JSON.stringify(context.MY_GROUPS)), [{groupId: 4, groupName: 'Admins'}, {groupId: 5, groupName: 'Members'}])
})

test('telephony and sessions load together while workspace status renders independently', async () => {
  const pending = new Map(), rendered = []
  const context = vm.createContext({
    allSessions: [], voxWorkspaceAvailable: false, currentView: {type: 'app'},
    fetch: url => new Promise((resolve, reject) => pending.set(url, {resolve, reject})),
    renderBoardList: rows => rendered.push(rows), applyDisabledApps() {}
  })
  vm.runInContext(source.slice(source.indexOf('let sessionLoadGeneration'), source.indexOf('// ─── Workspace icons')), context)
  const loaded = context.loadSessions()
  assert.deepEqual([...pending.keys()], ['/api/sessions', '/api/vox-workspace'])
  pending.get('/api/sessions').resolve({ok: true, json: async () => ({sessions: [{id: 'main', runStatus: 'running'}]})})
  await new Promise(setImmediate)
  assert.equal(rendered.length, 1)
  assert.equal(rendered[0][0].runStatus, 'running')
  pending.get('/api/vox-workspace').reject(new Error('unavailable'))
  await loaded
  assert.equal(context.voxWorkspaceAvailable, false)
})
