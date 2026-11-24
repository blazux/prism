const {test}=require('node:test');
const assert=require('node:assert/strict');
const {readFileSync}=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const source=readFileSync(path.join(__dirname,'../apps/email-drafts.js'),'utf8');
const html=readFileSync(path.join(__dirname,'../apps/email.html'),'utf8');
const turn=()=>new Promise(resolve=>setImmediate(resolve));

function fixture(post) {
 const classes=new Set(['open']),fields={};
 for(const id of ['to','cc','bcc','subject','body','file','status'])fields['c-'+id]={value:'',textContent:'',classList:{toggle(){}}};
 for(const id of ['send-mail','discard-draft','draft-save-copy','draft-count','c-err'])fields[id]={};
 fields.overlay={classList:{contains:name=>classes.has(name),remove:name=>classes.delete(name)}};
 const writes=[];let token=0;
 const ctx=vm.createContext({crypto:{randomUUID:()=>`unique-client-token-${++token}`},setTimeout:()=>1,clearTimeout(){},addEventListener(){},
  document:{getElementById:id=>fields[id],querySelectorAll:()=>[],addEventListener(){}},publishMailContext(){},
  PrismModal:{confirm:async()=>true},PrismAPI:{toast(){},body:(method,body)=>({method,body}),api:async(url,opts)=>{
   if(!opts)return {drafts:[]};writes.push(opts.body);return post(opts.body,writes.length);
  }}});
 vm.runInContext("let composeGeneration=1,composeAtts=[],replyUid=17,replyFolder='Projects',sendingMail=false,cfg={};",ctx);
 vm.runInContext(source,ctx);
 const closeStart=html.indexOf('async function closeCompose(');
 vm.runInContext(html.slice(closeStart,html.indexOf('\nfunction publishMailContext()',closeStart)),ctx);
 const run=s=>vm.runInContext(s,ctx);
 fields['c-to'].value='recipient@example.test';fields['c-body'].value='Initial draft';run('resetDraftState()');
 return {ctx,run,fields,writes,classes};
}
const saved=(body,revision)=>({draft:{...body,id:12,revision}});

test('typing during a save is serialized into the next revision without replacing local text',async()=>{
 let finish;
 const f=fixture((body,n)=>n===1?new Promise(resolve=>finish=()=>resolve(saved(body,1))):saved(body,2));
 const first=f.run('flushDraft()');await turn();
 f.fields['c-body'].value='Newer typing';const second=f.run('flushDraft()');
 assert.equal(f.writes.length,1);finish();await Promise.all([first,second]);
 assert.equal(f.writes.length,2);assert.equal(f.writes[1].id,12);assert.equal(f.writes[1].revision,1);
 assert.equal(f.writes[1].body,'Newer typing');assert.equal(f.fields['c-body'].value,'Newer typing');
 assert.equal(f.fields['c-status'].textContent,'Saved in Prism');
 assert.equal(f.writes[0].folder,'Projects');assert.equal(f.writes[0].uid,17);
});
test('text-only saves retain persisted attachments instead of uploading an empty replacement',async()=>{
 const f=fixture((body,n)=>saved(body,n));await f.run('flushDraft()');
 assert.equal(JSON.stringify(f.writes[0].attachments),'[]');
 f.fields['c-body'].value='Changed text';await f.run('flushDraft()');
 assert.equal(Object.hasOwn(f.writes[1],'attachments'),false);
});
test('a stale save leaves local edits intact and blocks automatic overwrites',async()=>{
 const f=fixture(()=>{const error=Error('Another window edited this draft');error.status=409;throw error;});
 await assert.rejects(f.run('flushDraft()'),/Another window/);
 assert.equal(f.fields['c-body'].value,'Initial draft');assert.equal(f.fields['draft-save-copy'].hidden,false);
 await assert.rejects(f.run('flushDraft()'),/changed elsewhere/);assert.equal(f.writes.length,1);
});
test('replayed creation is recoverable without overwriting the acknowledged version',async()=>{
 const f=fixture(body=>({...saved(body,4),replayed:true}));
 await assert.rejects(f.run('flushDraft()'),/earlier save was received/);
 assert.equal(f.run('draftState.id'),12);assert.equal(f.run('draftState.conflict'),true);
 assert.equal(f.fields['c-body'].value,'Initial draft');assert.equal(f.writes.length,1);
});
test('closing after a storage error keeps the composer and its unsaved text',async()=>{
 const f=fixture(()=>{throw Error('Storage unavailable');});
 assert.equal(await f.run('closeCompose()'),false);
 assert.equal(f.classes.has('open'),true);assert.equal(f.fields['c-body'].value,'Initial draft');
 assert.match(f.fields['c-err'].textContent,/Storage unavailable/);
});
for(const changed of [false,true])test(`copying a draft ${changed?'to a new mailbox clears':'within its mailbox retains'} the reply UID`,async()=>{
 const f=fixture(body=>saved(body,1));
 f.run(`draftState.id=10;draftState.revision=2;draftState.blocked=true;draftState.mailboxChanged=${changed}`);
 await f.run('saveDraftCopy()');
 assert.equal(f.writes.length,1);assert.equal(f.writes[0].id,0);
 assert.equal(f.writes[0].uid,changed?0:17);assert.equal(f.writes[0].folder,changed?'INBOX':'Projects');
 assert.equal(f.writes[0].body,'Initial draft');
});
