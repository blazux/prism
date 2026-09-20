const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),{webcrypto}=require('node:crypto');
function fixture(options={}){
 let state={app:'email',id:'1',to:'a@example.invalid',subject:'Test',body:'Hello',readonly:false};
 const listeners={},messages=[],parent={postMessage:d=>messages.push(d)},origin='http://localhost';
 const ctx=vm.createContext({window:{},TextEncoder,crypto:webcrypto,parent,location:{origin},document:{addEventListener(){}},addEventListener:(k,fn)=>listeners[k]=fn});
 vm.runInContext(fs.readFileSync(path.join(__dirname,'../editor-bridge.js'),'utf8'),ctx);
 const bridge=ctx.window.PrismEditor.connect({read:()=>state,fields:options.fields||['to','subject','body'],types:options.types,context:s=>'Editing '+s.id,idleContext:()=> 'Reading inbox',update:async p=>Object.assign(state,p)});
 async function call(args,extra={}){messages.length=0;await listeners.message({source:parent,origin,data:{type:'editor-request',id:'request',expires_at:Date.now()+30000,args},...extra});return messages.find(x=>x.type==='editor-response')?.result;}
 return {bridge,call,messages,set:s=>state=s,get:()=>state,parent,origin};
}
test('read and patch active draft, preserving omitted fields; publish/close context',async()=>{
 const f=fixture();f.bridge.publish();assert.match(f.messages[0].text,/Editing 1/);
 const read=await f.call({action:'read'});assert.equal(read.body,'Hello');
 const result=await f.call({action:'update',revision:read.revision,body:'Polished'});
 assert.equal(result.status,'updated');assert.equal(f.get().to,'a@example.invalid');assert.equal(f.get().body,'Polished');
 f.set(null);f.bridge.publish();assert.equal(f.messages.at(-1).text,'Reading inbox');
});
test('typing, switching or closing after read rejects a stale write',async()=>{
 for(const change of [s=>({...s,body:'My new words'}),s=>({...s,id:'other'}),()=>null]){
  const f=fixture(),r=await f.call({action:'read'});f.set(change(f.get()));const before=JSON.stringify(f.get());
  const result=await f.call({action:'update',revision:r.revision,body:'stale'});assert.match(result.error,/changed/);assert.equal(JSON.stringify(f.get()),before);
 }
});
test('read-only, invalid fields and expired requests cannot modify the editor',async()=>{
 const f=fixture();let read=await f.call({action:'read'});
 assert.match((await f.call({action:'update',revision:read.revision,body:'wrong',send:true})).error,/Unsupported/);assert.equal(f.get().body,'Hello');
 f.get().readonly=true;read=await f.call({action:'read'});
 assert.match((await f.call({action:'update',revision:read.revision,body:'wrong'})).error,/read-only/);
 const expired=await f.call({}, {data:{type:'editor-request',id:'old',expires_at:0,args:{action:'read'}}});assert.match(expired.error,/expired/);
 assert.equal(await f.call({action:'read'},{origin:'https://evil.invalid'}),undefined);
 assert.equal(await f.call({action:'read'},{source:{}}),undefined);
});
test('long Unicode bodies paginate completely with a stable revision',async()=>{
 const f=fixture();f.get().body='📧é漢"\n'.repeat(6000);let body='',offset=0,rev;
 for(let i=0;i<20;i++){const r=await f.call({action:'read',offset});assert.ok(!r.error,r.error);assert.ok(new TextEncoder().encode(JSON.stringify(r)).length<=18000);rev??=r.revision;assert.equal(r.revision,rev);body+=r.body;if(!r.truncated)break;assert.ok(r.next_offset>offset);offset=r.next_offset;}
 assert.equal(body,f.get().body);
});

test('typed calendar fields accept booleans and reject string flags atomically',async()=>{
 const f=fixture({fields:['all_day','body'],types:{all_day:'boolean'}});const r=await f.call({action:'read'});
 assert.match((await f.call({action:'update',revision:r.revision,all_day:'true',body:'wrong'})).error,/Unsupported/);assert.equal(f.get().body,'Hello');
 assert.equal((await f.call({action:'update',revision:r.revision,all_day:true})).status,'updated');assert.equal(f.get().all_day,true);
});
