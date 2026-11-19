const {test} = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm')
const web = name => fs.readFileSync(path.join(__dirname, '..', name), 'utf8')
const source = web('app.js')
function links(search = '') {
  const clicks = [], messages = [], listeners = [], win = {}
  win.parent = win
  const ctx = vm.createContext({window:win, parent:win, location:{origin:'https://prism.test',search}, URL, URLSearchParams,
    document:{addEventListener:(name,fn)=>clicks.push(fn),removeEventListener(){}}, addEventListener:(name,fn)=>listeners.push(fn)})
  vm.runInContext(web('object-links.js'), ctx)
  return {ctx, api:win.PrismLinks, clicks, listeners, messages}
}
const plain = value => JSON.parse(JSON.stringify(value))
test('ordinary links round-trip opaque IDs without credentials or owner selection', () => {
  const {api} = links()
  for (const name of ['notes','tasks','calendar']) {
    const id = 'Folder/A & B #é?.md'
    assert.deepEqual(plain(api.parse(api.href({type:'app',name,id}))), {type:'app',name,id})
  }
  assert.deepEqual(plain(api.parse('/?app=email&id=153&folder=Archive%2F2024&user=99&session=u99-main')), {type:'app',name:'email',id:'153',folder:'Archive/2024'})
  assert.deepEqual(plain(api.parse('/?workspace=u7-telegram&chat=open')), {type:'board',workspace:'u7-telegram',chat:'open'})
  assert.equal(api.href({type:'board',workspace:'Finance & docs',chat:'open'}), '/?workspace=Finance+%26+docs&chat=open')
})
test('external, malformed and unsupported destinations cannot drive an app or change settings scope', () => {
  const {api} = links()
  for (const href of ['javascript:alert(1)','https://evil.test/?app=notes&id=1','//evil.test/?app=notes&id=1','/api/notes?id=1','/?app=admin&id=1','/?app=email&id=0','/?app=email&id=4294967296','/?app=notes&id=','/?app=notes&id=%00x','/?app=notes&id=1&id=2','/?app=notes&folder=INBOX','/?view=settings&workspace=u99-private']) {
    assert.equal(api.parse(href), null, href)
  }
  assert.equal(api.parse('/?app=telephony&tab=logs'), null) // Existing telephony route owns this URL.
})
test('normal clicks open internal references in place while modifiers and downloads retain browser behavior', () => {
  const f = links(), opened = []
  f.api.bind(view => opened.push(plain(view)))
  const click = (href, extras = {}) => {
    let prevented = false
    const anchor = {getAttribute:()=>href,hasAttribute:()=>!!extras.download}
    f.clicks[0]({button:0,target:{closest:()=>anchor},preventDefault(){prevented=true},...extras})
    return prevented
  }
  assert.equal(click('/?app=notes&id=42'), true)
  assert.equal(click('/?app=notes&id=42',{ctrlKey:true}), false)
  assert.equal(click('/?app=notes&id=42',{download:true}), false)
  assert.equal(click('https://example.org/'), false)
  assert.deepEqual(opened, [{type:'app',name:'notes',id:'42'}])
})
test('app references wait for readiness and reject messages from other windows or origins', async () => {
  const f = links('?id=first'), received = []
  const ready = f.api.receive('notes', (view,current) => received.push({id:view.id,current}))
  const send = (id,extra={}) => f.listeners[0]({source:f.ctx.parent,origin:'https://prism.test',data:{type:'open-reference-object',href:'/?app=notes&id='+id},...extra})
  send('second');send('foreign',{source:{}});send('external',{origin:'https://evil.test'})
  assert.equal(received.length,0)
  ready();await Promise.resolve()
  assert.equal(received[0].id,'second')
  send('third');await Promise.resolve()
  assert.equal(received[0].current(),false)
  assert.equal(received[1].current(),true)
})

function shell(options = {}) {
  const f = links(), views = [], notices = [], messages = []
  const frame = {contentWindow:{postMessage:(...args)=>messages.push(args),PrismLinks:options.beforeLeave?{beforeLeave:options.beforeLeave}:undefined}}
  const c = vm.createContext({window:{PrismLinks:f.api},ME:{uid:7},allSessions:options.sessions||[],currentView:options.view||{type:'board'},
    activeEditorSource:options.ready ? frame.contentWindow : null, document:{getElementById:()=>frame}, location:{origin:'https://prism.test'},
    showToast:value=>notices.push(value),fetch:options.fetch||(()=>{throw Error('unexpected fetch')}),
    setView:view=>{views.push(plain(view));c.currentView=view},chatOpen:false,toggleChat:()=>{c.chatOpen=true}})
  const personal = source.slice(source.indexOf('function personalSessionID('), source.indexOf('function gotoSession('))
  const start = source.indexOf('let referenceNavigation = 0')
  vm.runInContext(personal + source.slice(start, source.indexOf('window.PrismLinks?.bind', start)), c)
  return {c,views,notices,messages,open:view=>c.openPrismReference(view)}
}
test('workspace links resolve hidden conversations read-only and never create an unknown workspace', async () => {
  const paths=[]
  const f=shell({fetch:async path=>{paths.push(path);return {ok:true,json:async()=>({sessions:[{id:'u7-telegram'}]})}}})
  assert.equal(await f.open({type:'board',workspace:'telegram',chat:'open'}),true)
  assert.deepEqual(paths,['/api/sessions?includeSystem=1'])
  assert.equal(f.views[0].workspace,'u7-telegram');assert.equal(f.c.chatOpen,true)
  assert.equal(await f.open({type:'board',workspace:'u99-secret'}),false)
  assert.equal(f.views.length,1);assert.equal(f.notices.length,1)
})
test('a late workspace lookup does not override a newer user navigation', async () => {
  let finish
  const f=shell({fetch:()=>new Promise(resolve=>finish=resolve)})
  const opening=f.open({type:'board',workspace:'old'})
  f.c.currentView={type:'settings'}
  finish({ok:true,json:async()=>({sessions:[{id:'u7-old'}]})});await opening
  assert.equal(f.views.length,0)
})
test('deleted cached workspaces and canceled or stale draft guards never navigate', async () => {
  const stale=shell({sessions:[{id:'u7-deleted'}],fetch:async()=>({ok:true,json:async()=>({sessions:[]})})})
  assert.equal(await stale.open({type:'board',workspace:'deleted'}),false)
  assert.equal(stale.views.length,0)
  const canceled=shell({view:{type:'app',name:'email'},beforeLeave:async()=>false})
  assert.equal(await canceled.open({type:'app',name:'notes',id:'42'}),false)
  assert.equal(canceled.views.length,0)
  let finish
  const pending=shell({view:{type:'app',name:'notes'},beforeLeave:()=>new Promise(resolve=>finish=resolve)})
  const opening=pending.open({type:'app',name:'tasks',id:'17'})
  pending.c.currentView={type:'settings'}
  finish(true);await opening
  assert.equal(pending.views.length,0)
})
test('discard confirmation preserves edits made while it is open and clipboard fallback exposes the same URL', async () => {
  const f=links(),prompts=[],copied=[]
  let draft='saved',finish,confirmations=0
  f.ctx.window.PrismModal={confirm:()=>{confirmations++;return new Promise(resolve=>finish=resolve)},prompt:async(...args)=>prompts.push(args)}
  assert.equal(await f.api.confirmLeave(()=>draft,'saved'),true)
  assert.equal(confirmations,0)
  draft='first edit'
  const leave=f.api.confirmLeave(()=>draft,'saved')
  draft='newer edit';finish(true)
  assert.equal(await leave,false)
  f.ctx.navigator={clipboard:{writeText:async value=>copied.push(value)}}
  await f.api.copy({type:'app',name:'notes',id:'Folder/A & B.md'})
  assert.equal(copied[0],'https://prism.test/?app=notes&id=Folder%2FA+%26+B.md')
  f.ctx.navigator.clipboard.writeText=async()=>{throw Error('not permitted')}
  await f.api.copy({type:'app',name:'tasks',id:'17'})
  assert.equal(prompts[0][1][0].value,'https://prism.test/?app=tasks&id=17')
})
test('opening another object in the ready app preserves the iframe and its editor', async () => {
  const f=shell({view:{type:'app',name:'notes'},ready:true})
  await f.open({type:'app',name:'notes',id:'42'})
  assert.equal(f.views.length,0)
  assert.equal(f.messages[0][0].type,'open-reference-object')
  assert.equal(f.messages[0][0].href,'/?app=notes&id=42')
  assert.equal(f.messages[0][1],'https://prism.test')
})
function appFunction(file,name) {
  const html=web('apps/'+file+'.html'),start=html.indexOf('async function '+name+'(')
  assert.ok(start>=0,name)
  return html.slice(start,html.indexOf('\n}',start)+2)
}
test('calendar links retrieve an event beyond the displayed range without replacing an active form', async () => {
  const event={id:'remote/id',title:'Far future',startAt:'2099-06-15T10:00:00Z'},opened=[],paths=[]
  let dirty=false
  const c=vm.createContext({PrismAPI:{api:async path=>{paths.push(path);return {event}}},$ :()=>({classList:{contains:()=>dirty}}),
    calendarEditorGeneration:0,loadGeneration:0,eventStart:event=>new Date(event.startAt),view:new Date(),openEvent:event=>opened.push(event),
    load:async()=>{c.loadGeneration++}})
  vm.runInContext(appFunction('calendar','openLinkedEvent'),c)
  await c.openLinkedEvent({id:'remote/id'},()=>true)
  assert.deepEqual(paths,['/api/events?id=remote%2Fid']);assert.equal(opened[0],event)
  assert.equal(c.view.getUTCFullYear(),2099)
  dirty=true
  await assert.rejects(c.openLinkedEvent({id:'other'},()=>true),/Close the current event/)
  assert.equal(paths.length,1)
})
test('notes and task references use exact encoded IDs and preserve an existing draft/form', async () => {
  let canSave=true
  const selected=[],requests=[],note={id:'Folder/A & B.md',title:'Target'},c=vm.createContext({
    notes:[],selectedId:null,aiEditVersion:0,saveNote:async()=>canSave,api:async path=>{requests.push(path);return {note}},select:async id=>selected.push(id)})
  vm.runInContext(appFunction('notes','openLinkedNote'),c)
  await c.openLinkedNote({id:note.id},()=>true)
  assert.equal(requests[0],'?id=Folder%2FA%20%26%20B.md');assert.deepEqual(selected,[note.id])
  canSave=false
  await c.openLinkedNote({id:'Another note'},()=>true)
  assert.equal(requests.length,1);assert.deepEqual(selected,[note.id])
  const t=vm.createContext({taskEditForm:{dialog:{open:true}},taskCreateActive:false})
  vm.runInContext(appFunction('tasks','openLinkedTask'),t)
  await assert.rejects(t.openLinkedTask({id:'1'},()=>true),/Close the current task form/)
})
test('mail references retrieve a UID outside the loaded page in its exact folder', async () => {
  const reads=[],c=vm.createContext({cfg:{configured:true},folder:'INBOX',readingGeneration:0,
    document:{getElementById:id=>id==='mail'?{}:{classList:{contains:()=>false}}},
    loadInbox:async()=>{c.readingGeneration++},openMessage:async uid=>reads.push([c.folder,uid])})
  vm.runInContext(appFunction('email','openLinkedMessage'),c)
  await c.openLinkedMessage({id:'153',folder:'Archive/2024'},()=>true)
  assert.deepEqual(reads,[['Archive/2024',153]])
})
