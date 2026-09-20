const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {readFileSync}=require('node:fs');
const path=require('node:path');
const html=readFileSync(path.join(__dirname,'../apps/email.html'),'utf8');
const source=html.slice(html.indexOf('let triageState='),html.indexOf('function compose()'));
function fixture({tags={},pages=[],onAssist}={}){
 const writes=[],offsets=[],batches=[];
 let ctx;
 ctx=vm.createContext({document:{querySelectorAll:()=>[],getElementById:()=>null},PrismAPI:{toast:()=>{},body:(method,body)=>({method,body}),api:async(url,options)=>{
  if(options){writes.push({url,body:options.body});return {};}
  if(url.startsWith('/api/email/tags'))return {...tags};
  const offset=+new URL('http://test'+url).searchParams.get('offset');offsets.push(offset);return {messages:pages[offset/100]||[]};
 }},renderList:()=>{},senderName:s=>s,assist:async(_,raw)=>{const batch=JSON.parse(raw);batches.push(batch);if(onAssist)onAssist(ctx);return JSON.stringify(batch.map(m=>({i:m.i,category:'FYI',tags:['review']})));}});
 vm.runInContext("let folder='Projects',mailTags={},current={uid:7,from:'a@example.org',subject:'Example'};"+source,ctx);
 return {ctx,writes,offsets,batches};
}
test('all categorizes older pages and skips already categorized messages',async()=>{
 const page=Array.from({length:100},(_,i)=>({uid:i+1,subject:'Message '+i,from:'a@example.org'}));
 const f=fixture({tags:{1:{category:'FYI'}},pages:[page,[{uid:101,subject:'Older',from:'a@example.org'}]]});
 await vm.runInContext("triage('all')",f.ctx);
 assert.deepEqual(f.offsets,[0,100]);assert.equal(f.batches.reduce((n,b)=>n+b.length,0),100);
 assert.ok(f.batches.every(b=>b.length<=20));assert.ok(f.writes.every(w=>w.url.endsWith('folder=Projects')));
 assert.ok(f.writes.some(w=>w.body[101]));assert.ok(f.writes.every(w=>!w.body[1]));
});
test('single-message categorization stays scoped when user changes folders',async()=>{
 const f=fixture({tags:{7:{category:'Newsletter'}},onAssist:ctx=>vm.runInContext("folder='INBOX'",ctx)});
 await vm.runInContext("triage('message')",f.ctx);
 assert.equal(f.offsets.length,0);assert.equal(f.writes.length,1);assert.ok(f.writes[0].url.endsWith('folder=Projects'));
 assert.equal(f.writes[0].body[7].category,'FYI');assert.equal(vm.runInContext('Object.keys(mailTags).length',f.ctx),0);
});
test('stop persists current batch and skips remaining requests',async()=>{
 const f=fixture({pages:[Array.from({length:100},(_,i)=>({uid:i+1,subject:'Test',from:'a@example.org'}))],onAssist:ctx=>vm.runInContext('stopTriage()',ctx)});
 await vm.runInContext("triage('all')",f.ctx);
 assert.equal(f.batches.length,1);assert.equal(Object.keys(f.writes[0].body).length,20);
 assert.equal(vm.runInContext('triageState',f.ctx),null);assert.match(vm.runInContext('triageStatus',f.ctx),/stopped/);
});
