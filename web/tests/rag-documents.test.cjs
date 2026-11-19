const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const tick=()=>new Promise(setImmediate);
function fixture(file){
 const source=fs.readFileSync(path.join(__dirname,'../documents.js'),'utf8');
 function fn(name){const start=source.search(new RegExp('(?:async )?function '+name+'\\('));assert.ok(start>=0,name);return source.slice(start,source.indexOf('\n    }',start)+6).replace(/^    /gm,'');}
 function makeArea(){let html='',buttons=[];return {get innerHTML(){return html},set innerHTML(value){html=value;buttons=[...value.matchAll(/class="doc-del-btn"[^>]*data-id="(\d+)"/g)].map(m=>({dataset:{id:m[1]},disabled:false,addEventListener(type,cb){this.click=cb}}))},set textContent(value){html=value;buttons=[]},querySelectorAll:()=>buttons};}
 let area=makeArea();const requests=[],notices=[];
 const ctx=vm.createContext({fetch:(url,options)=>new Promise((resolve,reject)=>requests.push({url,options,resolve,reject})),settingsFetch:(url,options)=>new Promise((resolve,reject)=>requests.push({url,options,resolve,reject})),document:{getElementById:id=>id==='docs-area'?area:null},getById:id=>id==='docs-area'?area:null,active:()=>true,request:(url,options)=>new Promise((resolve,reject)=>requests.push({url,options,resolve,reject})),renderCollections(){},renderContent(){area=makeArea()},renderRAGContent(){area=makeArea()},renderContentEmpty(){area=makeArea()},showToast:message=>notices.push(message),escHtml:String,fmtSize:String,fmtDate:String,SESSION:'test',CAN_MANAGE:true,RAG_MANAGE:true,PrismModal:{confirm:async()=>true}});
 vm.runInContext("let currentCollection=null,collections=[{name:'A',description:'old'},{name:'B'}],documentsRequestVersion=0,collectionsRequestVersion=0,displayedDocuments=null;"+['requireRAGSuccess','selectCollection','loadDocuments','renderDocuments','deleteDocument','loadCollections','saveDescription'].map(fn).join('\n'),ctx);
 return {requests,notices,area:()=>area,run:s=>vm.runInContext(s,ctx)};
}
for(const file of ['shared Documents']){
 test(file+': late collection A documents never appear under B or authorize a stale deletion',async()=>{
  const f=fixture(file);f.run("selectCollection('A')");const oldArea=f.area();f.run("selectCollection('B')");
  f.requests[1].resolve(new Response(JSON.stringify([{id:2,filename:'B document'}])));await tick();assert.match(f.area().innerHTML,/B document/);assert.doesNotMatch(f.area().innerHTML,/A document/);
  f.requests[0].resolve(new Response(JSON.stringify([{id:1,filename:'A document'}])));await tick();assert.match(f.area().innerHTML,/B document/);assert.notEqual(f.area(),oldArea);
  await f.run('deleteDocument(1)');assert.equal(f.requests.length,2);
  const snapshot=f.run('displayedDocuments');f.run("selectCollection('A')");await f.run('deleteDocument(2, null)');assert.equal(f.requests.length,3);
  // A detached B button retains its successful snapshot, which is now invalid.
  const detached=snapshot.area.querySelectorAll()[0];await detached.click();assert.equal(f.requests.length,3);
  f.requests[2].resolve(new Response('[]'));await tick();
 });
 test(file+': failed document reads preserve displayed rows and disable deletion until a successful reload',async()=>{
  const f=fixture(file);f.run("selectCollection('A')");f.requests[0].resolve(new Response('[{"id":1,"filename":"Existing"}]'));await tick();
  const refresh=f.run('loadDocuments()');f.requests[1].resolve(new Response('{"error":"Forbidden"}',{status:403}));await refresh;
  assert.match(f.area().innerHTML,/Existing/);assert.equal(f.area().querySelectorAll()[0].disabled,true);assert.match(f.notices.at(-1),/Forbidden/);await f.run('deleteDocument(1)');assert.equal(f.requests.length,2);
 });
 test(file+': rejected deletes keep the valid row and allow retry; descriptions target the captured collection',async()=>{
  const f=fixture(file);f.run("selectCollection('A')");f.requests[0].resolve(new Response('[{"id":1,"filename":"Existing"}]'));await tick();
  const remove=f.run('deleteDocument(1)');await f.run('deleteDocument(1)');assert.equal(f.requests.length,2);f.requests[1].resolve(new Response('Cannot delete',{status:403}));await remove;
  assert.match(f.area().innerHTML,/Existing/);assert.equal(f.area().querySelectorAll()[0].disabled,false);assert.match(f.notices.at(-1),/Cannot delete/);
  f.run("currentCollection='B'");const save=f.run("saveDescription('new', 'A')");assert.equal(JSON.parse(f.requests[2].options.body).name,'A');f.requests[2].reject(Error('offline'));await save;assert.equal(f.run('collections[0].description'),'old');assert.match(f.notices.at(-1),/offline/);
 });
}
for(const file of ['shared Documents'])test(file+': an upload batch stays bound to its original collection after selection changes',async()=>{
 const source=fs.readFileSync(path.join(__dirname,'../documents.js'),'utf8'),start=source.indexOf('async function uploadFiles(files) {');
 const classes={add(){},remove(){}},oldInput={value:'selected'},newInput={value:'new selected'},requests=[];let input=oldInput,refreshes=0;
 const elements={'upload-zone':{classList:classes},'upload-progress':{classList:classes,textContent:''},'upload-collection':{value:'A'}};
 class FakeFormData{constructor(){this.fields={}}append(name,value){this.fields[name]=value}}
 const fetch=(url,options)=>new Promise(resolve=>requests.push({url,options,resolve}));
 const ctx=vm.createContext({document:{getElementById:id=>id==='upload-input'?input:elements[id]},getById:id=>id==='upload-input'?input:elements[id],request:(url,options)=>new Promise(resolve=>requests.push({url,options,resolve})),RAG_MANAGE:true,FormData:FakeFormData,fetch,settingsFetch:fetch,showToast(){},pollIngestProgress(){},active:()=>true,loadDocuments:async()=>refreshes++,loadCollections:async()=>{},SESSION:'test'});
 vm.runInContext("let currentCollection='A';"+source.slice(start,source.indexOf('\n    }',start)+6).replace(/^    /gm,''),ctx);
 const upload=vm.runInContext("uploadFiles([{name:'first.txt'},{name:'second.txt'}])",ctx);
 vm.runInContext("currentCollection='B'",ctx);input=newInput;requests[0].resolve(new Response('{"chunks":1}'));await tick();
 assert.equal(requests[1].options.body.fields.collection,'A');requests[1].resolve(new Response('{"chunks":1}'));await upload;
 assert.equal(oldInput.value,'');assert.equal(newInput.value,'new selected');assert.equal(refreshes,0);
});
for(const file of ['shared Documents'])test(file+': overlapping reads of the same collection display only the newest result',async()=>{
 const f=fixture(file);f.run("selectCollection('A')");const newer=f.run('loadDocuments()');
 f.requests[1].resolve(new Response('[{"id":2,"filename":"Latest"}]'));await newer;
 f.requests[0].resolve(new Response('[{"id":1,"filename":"Outdated"}]'));await tick();
 assert.match(f.area().innerHTML,/Latest/);assert.doesNotMatch(f.area().innerHTML,/Outdated/);await f.run('deleteDocument(1)');assert.equal(f.requests.length,2);
});

test('both page hosts use the common Documents component without workspace scope',()=>{
 for(const name of ['settings.html','rag.html']){
  const host=fs.readFileSync(path.join(__dirname,'../'+name),'utf8');
  assert.match(host, /src="\/documents\.js"/);assert.match(host,/href="\/documents\.css"/);
  assert.match(host,/PrismDocuments.mount\(container/);
  assert.doesNotMatch(host,/function (?:loadDocuments|uploadFiles|deleteDocument)\(/);
 }
 const legacy=fs.readFileSync(path.join(__dirname,'../rag.html'),'utf8');
 assert.doesNotMatch(legacy,/URLSearchParams|SESSION|session-badge/);
});

test('Documents permissions follow account/group identity and fail closed',async()=>{
 const source=fs.readFileSync(path.join(__dirname,'../documents.js'),'utf8');
 const ctx=vm.createContext({window:{}});vm.runInContext(source,ctx);
 for(const entry of [
  {me:{authenticated:true,multiUser:false},groups:[],manage:true,scoped:true},
  {me:{authenticated:true,multiUser:true,user:{role:'member'}},groups:[{id:2}],manage:false,scoped:true},
  {me:{authenticated:true,multiUser:true,user:{role:'global_admin'}},groups:[{id:2}],manage:false,scoped:true},
  {me:{authenticated:true,multiUser:true},groups:[],manage:false,scoped:false},
  {me:{authenticated:false,multiUser:true},groups:[],manage:false,scoped:false}
 ]){
  const urls=[];const scope=await ctx.window.PrismDocuments.permissions(async url=>{urls.push(url);return new Response(JSON.stringify(url==='/api/me'?entry.me:{groups:entry.groups}))});
  assert.equal(scope.manage,entry.manage);assert.equal(scope.scoped,entry.scoped);
  assert.equal(urls.includes('/api/my/groups'),entry.me.authenticated);
 }
 await assert.rejects(ctx.window.PrismDocuments.permissions(async()=>new Response('unavailable',{status:503})),/account permissions/);
});
