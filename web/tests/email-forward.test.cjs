const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const html=fs.readFileSync(path.join(__dirname,'../apps/email.html'),'utf8');
const drafts=fs.readFileSync(path.join(__dirname,'../apps/email-drafts.js'),'utf8');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fn(name){const start=html.search(new RegExp('(?:async )?function '+name+'\\('));assert(start>=0,name);return html.slice(start,html.indexOf('\n}',start)+2);}
function fixture(fetchAttachment=async()=>new Response('original')){
 const classes=new Set(),fields={},writes=[],requests=[];
 for(const key of ['to','cc','bcc','subject','body','file','status','err'])fields['c-'+key]={value:'',textContent:'',classList:{toggle(){}},focus(){},setSelectionRange(){}};
 for(const id of ['compose-title','send-mail','discard-draft','draft-save-copy','draft-count','forward-retry','forward-skip'])fields[id]={hidden:true};
 fields.overlay={classList:{contains:key=>classes.has(key),add:key=>classes.add(key),remove:key=>classes.delete(key)}};
 let timer=0;
 const context=vm.createContext({File,Blob,AbortController,URLSearchParams,Date,crypto:require('node:crypto').webcrypto,
  setTimeout:()=>++timer,clearTimeout(){},addEventListener(){},
  FileReader:class{readAsDataURL(file){file.arrayBuffer().then(bytes=>{this.result='data:'+file.type+';base64,'+Buffer.from(bytes).toString('base64');this.onload();},()=>this.onerror());}},
  document:{getElementById:id=>fields[id],querySelectorAll:()=>[],addEventListener(){}},
  fetch:async(url,options)=>{requests.push({url,options});assert(url.startsWith('/api/email/attachment?'),'No mail may be sent by this test');return fetchAttachment(url,options,requests.length);},
  PrismAPI:{toast(){},failure:response=>response.text(),body:(method,body)=>({method,body}),api:async(url,options)=>{
   if(url==='/api/email/config')return {account:'account-B'};
   if(!options)return {drafts:[]};assert.equal(url,'/api/email/drafts');writes.push(options.body);
   return {draft:{...options.body,id:9,revision:writes.length}};
  }},
  PrismModal:{confirm:async()=>true},publishMailContext(){},renderComposeAtts(){},looksHTML:()=>false,
  signatureText:()=>'',reSubject:subject=>'Re: '+subject,emailPlain:()=>'',assist:async()=> 'Suggested answer',
 });
 vm.runInContext("let composeGeneration=0,composeAtts=[],composeSnapshot='',folder='Projects & receipts',mailAccount='account-A',replyFolder='',replyUid=0,sendingMail=false,cfg={user:'me@example.test'},current={uid:17,from:'No reply <noreply@example.test>',replyTo:'help@example.test',to:'me@example.test, teammate@example.test',cc:'help@example.test, team@example.test',subject:'Résumé',body:'Original avec accents',date:'2026-11-26T10:00:00Z',attachments:[]};",context);
 vm.runInContext(drafts,context);
 vm.runInContext(['replyAddress','quotedReply','reply','replyAll','aiReply','forward','openCompose','closeCompose','doSend'].map(fn).join('\n'),context);
 return {fields,writes,requests,classes,run:source=>vm.runInContext(source,context)};
}

test('reply, reply all and AI draft honor Reply-To while retaining the original author in quoted text',async()=>{
 const f=fixture();f.run('reply()');assert.equal(f.fields['c-to'].value,'help@example.test');assert.match(f.fields['c-body'].value,/noreply@example.test/);
 await f.run('closeCompose(true)');f.run('replyAll()');assert.equal(f.fields['c-to'].value,'help@example.test, teammate@example.test');assert.equal(f.fields['c-cc'].value,'team@example.test');
 await f.run('closeCompose(true)');await f.run('aiReply()');assert.equal(f.fields['c-to'].value,'help@example.test');assert.match(f.fields['c-body'].value,/Suggested answer/);
 assert.equal(f.requests.length,0);
 assert.equal(f.run("replyAddress({replyTo:'bad\\r\\nvalue',from:'fallback@example.test'})"),'fallback@example.test');
 assert.equal(f.run("replyAddress({from:'fallback@example.test'})"),'fallback@example.test');
});

test('a forward loads every binary attachment before it can save or send, using the original mailbox reference',async()=>{
 const waiting=[];const f=fixture((url,options)=>new Promise(resolve=>waiting.push(resolve)));
 f.run("current.attachments=[{filename:'résumé.txt',contentType:'text/plain',size:4},{filename:'octets.bin',contentType:'application/octet-stream',size:4}]");
 const forwarding=f.run('forward()');f.fields['c-to'].value='recipient@example.test';
 assert.equal(f.fields['send-mail'].disabled,true);assert.equal(f.writes.length,0);
 f.fields['c-body'].value='Ma note personnelle';f.run('scheduleDraftSave()');
 const saving=f.run('flushDraft()');await f.run('doSend()');assert.equal(f.writes.length,0);
 f.run("folder='Another folder'");waiting[0](new Response('Déjà'));await tick();
 assert.equal(f.run('composeAtts.length'),0,'do not publish a partial set of attachments');assert.equal(f.writes.length,0);
 waiting[1](new Response(new Uint8Array([0,255,13,10])));
 assert.equal(await forwarding,true);await saving;
 assert.equal(f.fields['send-mail'].disabled,false);assert.equal(f.writes.length,1);assert.equal(f.writes[0].uid,0);
 assert.equal(f.writes[0].body,'Ma note personnelle');assert.equal(f.writes[0].attachments.length,2);
 assert.equal(f.writes[0].attachments[0].filename,'résumé.txt');assert.equal(Buffer.from(f.writes[0].attachments[0].data,'base64').toString(),'Déjà');
 assert.deepEqual([...Buffer.from(f.writes[0].attachments[1].data,'base64')],[0,255,13,10]);
 for(const request of f.requests){const query=new URL(request.url,'https://fixture.test').searchParams;assert.equal(query.get('folder'),'Projects & receipts');assert.equal(query.get('uid'),'17');assert.equal(query.get('account'),'account-A');}
});

test('a failed original attachment keeps the full forward unsent and unsaved until a complete retry succeeds',async()=>{
 let fail=true;const f=fixture(async url=>new URL(url,'https://fixture.test').searchParams.get('i')==='1'&&fail?new Response('Mailbox changed',{status:409}):new Response('Original'));
 f.run("current.attachments=[{filename:'first.txt',size:8},{filename:'second.txt',size:8}]");
 assert.equal(await f.run('forward()'),false);f.fields['c-body'].value='Keep this edited text';
 assert.equal(f.fields['send-mail'].disabled,true);assert.equal(f.fields['forward-retry'].hidden,false);assert.equal(f.run('composeAtts.length'),0);
  assert.equal(f.fields['forward-skip'].hidden,true,'changing the mailbox must not offer a way to bypass its attachment identity check');
 assert.equal(await f.run('closeCompose()'),false);assert.equal(f.classes.has('open'),true);assert.equal(f.writes.length,0);assert.match(f.fields['c-err'].textContent,/Mailbox changed/);
 await f.run('doSend()');assert.equal(f.requests.length,2);assert.equal(f.writes.length,0);
 fail=false;assert.equal(await f.run('retryForwardAttachments()'),true);await f.run('flushDraft()');
 assert.equal(f.run('composeAtts.length'),2);assert.equal(f.writes.length,1);assert.equal(f.writes[0].body,'Keep this edited text');assert.equal(f.fields['forward-retry'].hidden,true);
});

test('late attachment responses cannot change a newer composer after the old forward was closed',async()=>{
 let finish;const f=fixture(()=>new Promise(resolve=>finish=resolve));
 f.run("current.attachments=[{filename:'old.txt',size:3}]");const old=f.run('forward()');
 await f.run('closeCompose(true)');f.run("openCompose('New message','new@example.test','New subject','My new draft')");
 finish(new Response('old'));assert.equal(await old,false);
 assert.equal(f.fields['c-body'].value,'My new draft');assert.equal(f.fields['c-to'].value,'new@example.test');assert.equal(f.run('composeAtts.length'),0);assert.equal(f.fields['c-err'].textContent,'');assert.equal(f.fields['send-mail'].disabled,false);
});

test('closing a still-loading forward cannot later close or report errors in a replacement draft',async()=>{
 let finish;const f=fixture(()=>new Promise(resolve=>finish=resolve));
 f.run("current.attachments=[{filename:'old.txt',size:3}]");const old=f.run('forward()'),closing=f.run('closeCompose()');
 await f.run('closeCompose(true)');f.run("openCompose('New message','new@example.test','New subject','My replacement')");
 finish(new Response('old'));await old;assert.equal(await closing,false);
 assert.equal(f.classes.has('open'),true);assert.equal(f.fields['c-body'].value,'My replacement');assert.equal(f.fields['c-err'].textContent,'');
});

test('the attachment size limit refuses an incomplete forward without downloading or sending mail',async()=>{
 const f=fixture();f.run("current.attachments=[{filename:'huge.bin',size:21*1024*1024}]");
 assert.equal(await f.run('forward()'),false);assert.equal(f.requests.length,0);assert.equal(f.writes.length,0);assert.equal(f.fields['send-mail'].disabled,true);assert.match(f.fields['c-err'].textContent,/20 MB/);
});

test('omitting failed originals is explicit and retains the user text and their own attachments',async()=>{
 const f=fixture();f.run("current.attachments=[{filename:'huge.bin',size:21*1024*1024}]");
 await f.run('forward()');assert.equal(f.fields['forward-skip'].hidden,false);
 f.fields['c-body'].value='Keep this edited note';f.run("composeAtts.push(new File(['my file'],'mine.txt'));scheduleDraftSave(true)");
 f.run('PrismModal.confirm=async()=>false');await f.run('continueWithoutForwardAttachments()');assert.equal(f.fields['send-mail'].disabled,true);assert.equal(f.writes.length,0);
 f.run('PrismModal.confirm=async()=>true');await f.run('continueWithoutForwardAttachments()');await f.run('flushDraft()');
 assert.equal(f.fields['send-mail'].disabled,false);assert.equal(f.writes.length,1);assert.equal(f.writes[0].body,'Keep this edited note');assert.deepEqual([...f.writes[0].attachments].map(file=>file.filename),['mine.txt']);assert.equal(f.fields['forward-skip'].hidden,true);assert.equal(f.requests.length,0);
});

test('a mailbox change keeps an unsaved forward open and an explicit copy uses only the newly confirmed mailbox',async()=>{
 const f=fixture(async()=>new Response('Mailbox changed',{status:409}));
 f.run("current.attachments=[{filename:'original.txt',size:8}]");const pending=f.run('forward()');
 f.fields['c-body'].value='Keep my note for the new mailbox';f.run("composeAtts.push(new File(['my file'],'mine.txt'))");
 await pending;assert.equal(f.fields['draft-save-copy'].hidden,false);assert.equal(await f.run('closeCompose()'),false);assert.equal(f.classes.has('open'),true);assert.equal(f.writes.length,0);
 f.run('PrismModal.confirm=async()=>false');await f.run('saveDraftCopy()');assert.equal(f.writes.length,0);
 f.run('PrismModal.confirm=async message=>{if(!message.includes("original attachments could not be loaded"))throw Error("Missing attachment warning");return true;}');
 await f.run('saveDraftCopy()');
 assert.equal(f.writes.length,1);assert.equal(f.writes[0].account,'account-B');assert.equal(f.writes[0].uid,0);assert.equal(f.writes[0].folder,'INBOX');assert.equal(f.writes[0].body,'Keep my note for the new mailbox');assert.deepEqual([...f.writes[0].attachments].map(file=>file.filename),['mine.txt']);
 assert.equal(f.run('mailAccount'),'account-A','copying a draft must not retarget the still-loaded messages in the old mailbox');assert.equal(f.fields['send-mail'].disabled,false);
});

test('a draft-save mailbox conflict can be copied explicitly to the current mailbox without reusing its reply UID',async()=>{
 const f=fixture();
 f.run("reply();mailAccount='account-B';globalThis.copyWarnings=[];const originalAPI=PrismAPI.api;PrismAPI.api=async(url,options)=>{if(options?.body?.account==='account-A'){const error=Error('Mailbox changed');error.status=409;throw error;}return originalAPI(url,options);};PrismModal.confirm=async warning=>{copyWarnings.push(warning);return true;};");
 f.fields['c-body'].value='Keep my reply and its file';
 f.run("composeAtts.push(new File(['owned bytes'],'mine.txt'));scheduleDraftSave(true)");
 await assert.rejects(f.run('flushDraft()'),/Mailbox changed/);
 assert.equal(f.run('draftState.mailboxChanged'),false,'this starts as a save conflict, not an attachment download conflict');
 await f.run('saveDraftCopy()');
 assert.equal(f.writes.length,1);assert.equal(f.writes[0].account,'account-B');
 assert.equal(f.writes[0].uid,0);assert.equal(f.writes[0].folder,'INBOX');
 assert.equal(f.writes[0].body,'Keep my reply and its file');
 assert.deepEqual([...f.writes[0].attachments].map(file=>file.filename),['mine.txt']);
 assert.match(f.run('copyWarnings[0]'),/currently connected mailbox/);
 assert.equal(f.fields['send-mail'].disabled,false);
});

test('a mailbox lookup for an old copy cannot replace a newer composer',async()=>{
 const f=fixture();
 f.run("reply();PrismAPI.api=async(url,options)=>{if(url==='/api/email/config')return new Promise(resolve=>globalThis.resolveCopyConnection=resolve);if(options)throw Error('A stale copy attempted to save');return {drafts:[]};};PrismModal.confirm=async()=>{throw Error('A stale copy asked for confirmation');};");
 const copying=f.run('saveDraftCopy()');
 await f.run('closeCompose(true)');
 f.run("replyUid=0;openCompose('New message','new@example.test','New subject','My replacement text')");
 f.run("resolveCopyConnection({account:'account-B'})");
 await copying;
 assert.equal(f.fields['c-body'].value,'My replacement text');
 assert.equal(f.fields['c-to'].value,'new@example.test');
 assert.equal(f.fields['c-subject'].value,'New subject');
 assert.equal(f.writes.length,0);
});
