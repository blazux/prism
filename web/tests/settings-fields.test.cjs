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
test('AI connection tests leave edits unsaved; only a save binds the whole configuration form',async()=>{
 const source=fs.readFileSync(path.join(__dirname,'../ai-settings.js'),'utf8');
 const calls=[],controls=[{name:'baseURL'}];
 const context={payload:action=>({action}),form:{querySelectorAll:()=>controls},fetchConfig:async(...args)=>{calls.push(args);return {ok:true,text:async()=>'{}'}}};
 vm.createContext(context);vm.runInContext(source.slice(source.indexOf('  async function request(action)'),source.indexOf('  let monitorGeneration')),context);
 for(const action of ['models','test','embedding_models','embedding_test']){await context.request(action);assert.equal(calls.at(-1)[2],null,action+' is not a save');}
 await context.request('save');assert.equal(calls.at(-1)[2],controls);
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
