const {test}=require('node:test');
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
function fixture() {
  const events={},status={textContent:''};let response=new Response('{}');
  const context={window:{},document:{addEventListener:(name,fn)=>events[name]=fn,getElementById:()=>status},addEventListener:(name,fn)=>events[name]=fn,fetch:async()=>response};
  vm.createContext(context);vm.runInContext(fs.readFileSync(path.join(__dirname,'../settings-state.js'),'utf8'),context);
  const field=(id,original)=>({id,type:'textarea',value:original,isConnected:true,matches:()=>true});
  const edit=(el,text)=>{events.focusin({target:el});el.value=text;events.input({target:el})};
  return {state:context.window.PrismSettingsState,field,edit,status,events,setResponse:r=>response=r};
}
test('saving one form never clears equal unsaved values in another form',()=>{
  const {state,field,edit,status}=fixture(),base=field('base','base'),workspace=field('workspace','workspace');
  edit(base,'Réponds en français');edit(workspace,'Réponds en français');
  const saved=state.begin([base]);state.saved(saved);
  assert.equal(state.dirty(),true);assert.match(status.textContent,/other changes remain unsaved/);
  state.saved(state.begin([workspace]));assert.equal(state.dirty(),false);
});
test('edits made during a save remain dirty, and disconnected fields are ignored',()=>{
  const {state,field,edit}=fixture(),one=field('one','old');edit(one,'sent');const pending=state.begin([one]);edit(one,'newer');state.saved(pending);assert.equal(state.dirty(),true);
  edit(one,'sent');assert.equal(state.dirty(),false);edit(one,'newer');one.isConnected=false;assert.equal(state.dirty(),false);
});
test('a successful POST probe does not mark form edits saved',async()=>{
  const {state,field,edit,status}=fixture(),one=field('url','old');edit(one,'http://new/v1');
  await state.request('/api/ai/config',{method:'POST',body:JSON.stringify({action:'models',baseURL:one.value})});
  assert.equal(state.dirty(),true);assert.equal(status.textContent,'Unsaved changes');
});
test('failed saves retain edits and a successful explicit save clears only its controls',async()=>{
  const {state,field,edit,status,setResponse}=fixture(),one=field('one','old');edit(one,'new');
  setResponse(new Response('{}',{status:403}));await state.request('/save',{method:'POST'},[one]);assert.equal(state.dirty(),true);assert.match(status.textContent,/Could not save/);
  setResponse(new Response('{}'));await state.request('/save',{method:'POST'},[one]);assert.equal(state.dirty(),false);
});
function aiRequestFixture({fail=false}={}){
 const source=fs.readFileSync(path.join(__dirname,'../ai-settings.js'),'utf8');
 const start=source.indexOf('  async function requestData('),end=source.indexOf('  function setBusy('),performStart=source.indexOf('  async function perform(action)'),performEnd=source.indexOf('  const dialog = pane.querySelector');
 assert.ok(start>=0&&end>start&&performStart>=0&&performEnd>performStart,'extract the current staged request lifecycle');
 const calls=[],controls=[{name:'baseURL'},{name:'model'}],fields={embeddingMode:{value:'disabled'},apiKey:{value:'typed-key'},'embed-apiKey':{value:'typed-embedding-key'}};
 let loads=0;
 const context={payload:action=>({action}),form:{querySelectorAll:()=>controls,reportValidity:()=>true},f:name=>fields[name],
  status:{textContent:'',classList:{add(){},remove(){}}},pane:{querySelector:()=>({})},catalogKey:prefix=>prefix||'secondary',
  catalogs:new Map(),checks:new Map(),showModels(){},drawSources(){},confirm:async()=>true,
  fetchConfig:async(...args)=>{calls.push(args);return {ok:!fail,text:async()=>JSON.stringify(fail?{error:'Save refused'}:{models:['probe-model'],dimension:1024}),json:async()=>({})}},
  load:async()=>{loads++},
 };
 vm.createContext(context);vm.runInContext('let busy=false,resetPending=false,dirty=true,reloadRequired=false,sources=[{apiKey:"typed-key"}];function setBusy(v){busy=v}'+source.slice(start,end)+source.slice(performStart,performEnd),context);
 return {context,calls,controls,fields,loads:()=>loads,run:expr=>vm.runInContext(expr,context)};
}
test('AI probes leave edits unsaved; explicit save binds the form and clears submitted keys',async()=>{
 const f=aiRequestFixture();
 for(const action of ['models','test','embedding_models','embedding_test']){
  await f.context.perform(action);assert.equal(f.calls.at(-1)[2],null,action+' is not a save');
  assert.equal(f.run('dirty'),true);assert.equal(f.fields.apiKey.value,'typed-key');assert.equal(f.loads(),0);
 }
 await f.context.perform('save');assert.deepEqual(Array.from(f.calls.at(-1)[2]),f.controls);
 assert.equal(f.run('dirty'),false);assert.equal(f.fields.apiKey.value,'');assert.equal(f.fields['embed-apiKey'].value,'');assert.equal(f.loads(),1);
});
test('AI failed saves preserve typed keys and unsaved state',async()=>{
 const f=aiRequestFixture({fail:true});await f.context.perform('save');
 assert.equal(f.run('dirty'),true);assert.equal(f.fields.apiKey.value,'typed-key');assert.equal(f.fields['embed-apiKey'].value,'typed-embedding-key');
 assert.equal(f.loads(),0);assert.match(f.context.status.textContent,/Not saved: Save refused/);assert.deepEqual(Array.from(f.calls[0][2]),f.controls);
});
test('restoring AI defaults is staged until explicit Save changes',async()=>{
 const f=aiRequestFixture();await f.context.perform('reset');assert.equal(f.calls.length,0);assert.equal(f.run('resetPending'),true);
 await f.context.perform('save');assert.equal(f.calls.length,1);assert.equal(f.calls[0][0],'/api/ai/config?reindex=true');
 assert.equal(f.calls[0][1].method,'DELETE');assert.deepEqual(Array.from(f.calls[0][2]),f.controls);assert.equal(f.loads(),1);
});

test('a failed PIM autosave keeps selections and retries them without reporting success',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'../settings.html'),'utf8');
 const helper=html.slice(html.indexOf('async function settingsFetch('),html.indexOf('function getConfig()'));
 const autosave=html.slice(html.indexOf('  const saveSources = async () =>'),html.indexOf("  $('#src-cal').addEventListener('change', saveSources)"));
 const fields={'#src-cal':{value:'google'},'#src-task':{value:'todoist'}};
 const requests=[],statuses=[],toasts=[];let reloads=0,saves=0,fail=true;
 const context={
  $:id=>fields[id],
  PrismSettingsState:{begin:controls=>controls.map(el=>[el,el.value]),saved:()=>{saves++;statuses.push('Saved')},status:text=>statuses.push(text)},
  showToast:message=>toasts.push(message),
  fetch:async(url,options)=>{requests.push({url,body:JSON.parse(options.body)});return fail?new Response('{"error":"Provider save refused"}',{status:500}):new Response('{}')},
  loadSources:async()=>{reloads++;for(const id of Object.keys(fields))fields[id].value='server-refreshed'},
 };
 vm.createContext(context);vm.runInContext(helper+autosave+'\nglobalThis.runSaveSources=saveSources;',context);
 await context.runSaveSources();
 assert.equal(reloads,0,'failed save must not fetch and restore the old server values');
 assert.equal(fields['#src-cal'].value,'google');assert.equal(fields['#src-task'].value,'todoist');
 assert.equal(saves,0);assert.match(statuses.at(-1),/Could not save/);assert.equal(toasts.at(-1),'Provider save refused');
 fail=false;await context.runSaveSources();
 assert.equal(saves,1);assert.equal(reloads,1);assert.equal(statuses.at(-1),'Saved');
 assert.deepEqual(requests.map(r=>r.body),[{calendar:'google',tasks:'todoist'},{calendar:'google',tasks:'todoist'}]);
});
