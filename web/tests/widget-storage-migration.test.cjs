const {test} = require('node:test')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const runtime = import('data:text/javascript;base64,' + Buffer.from(fs.readFileSync(path.join(__dirname, '../widget-storage-migration.js'))).toString('base64'))
const store = entries => {
  const values = new Map(entries)
  return {getItem:key=>values.get(key) ?? null, setItem:(key,value)=>values.set(key,String(value))}
}

test('widget migration selects declared keys and keeps local/session storage distinct', async () => {
  const {legacyWidgetStorage} = await runtime
  const stores = {local:store([['custom-favourites','éléphant'],['custom-cache','cached']]),session:store([['custom-favourites','this tab']])}
  const source = `localStorage.getItem('custom-favourites'); sessionStorage.setItem('custom-favourites',''); const CACHE = 'custom-cache'; localStorage.getItem(CACHE);`
  const result = legacyWidgetStorage(source,'https://content.example.test',stores)
  assert.deepEqual({...result.local},{'custom-favourites':'éléphant','custom-cache':'cached'})
  assert.deepEqual({...result.session},{'custom-favourites':'this tab'})
})

test('widget source cannot export Prism preferences/history or arbitrary storage', async () => {
  const {legacyWidgetStorage} = await runtime
  const keys = ['prism-theme','Prism-approval','prism-widget-migrated:any','agent-config','active-workspace','active-session','history','chat-drawer-width','vox-supervision-session']
  const stores = {local:store(keys.map(key=>[key,'private fixture'])),session:store(keys.map(key=>[key,'private fixture']))}
  const source = keys.map(key=>`localStorage.getItem('${key}');sessionStorage.getItem('${key}');`).join('') + `Object.keys(localStorage);localStorage.getItem(String('history'));`
  const result = legacyWidgetStorage(source,'https://content.example.test',stores)
  assert.deepEqual({...result.local},{})
  assert.deepEqual({...result.session},{})
})

test('only acknowledged copied keys finish migration and original data stays available', async () => {
  const {legacyWidgetStorage,acknowledgeWidgetStorage} = await runtime
  const stores = {local:store([['custom-one','saved'],['custom-two','retry']]),session:store([])}
  const source = `localStorage.getItem('custom-one');localStorage.getItem('custom-two');`
  const origin = 'https://content.example.test', result = legacyWidgetStorage(source,origin,stores)
  acknowledgeWidgetStorage(result,{local:['custom-one','prism-theme','not-selected'],session:[]},origin,stores)
  assert.deepEqual({...legacyWidgetStorage(source,origin,stores).local},{'custom-two':'retry'})
  assert.equal(stores.local.getItem('custom-one'),'saved')
  assert.deepEqual({...legacyWidgetStorage(source,'https://other.example.test',stores).local},{'custom-one':'saved','custom-two':'retry'})
})
