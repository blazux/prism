const {test}=require('node:test');
const assert=require('node:assert/strict');
const {readFileSync}=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const html=readFileSync(path.join(__dirname,'../apps/email.html'),'utf8');
const send=html.slice(html.indexOf('async function doSend()'),html.indexOf('// Relative date bucket'));
function fixture(fetch){
 const fields={'c-body':{value:'Keep my draft'},'c-to':{value:'test@example.org'},'c-subject':{value:'Test'},'c-err':{},'send-mail':{}};
 let closed=0;
 const ctx=vm.createContext({FormData,fetch,document:{getElementById:id=>fields[id]},PrismAPI:{toast:()=>{}},closeCompose:async()=>{closed++;}});
 vm.runInContext("let sendingMail=false,replyUid=17,replyFolder='Projects & receipts',composeAtts=[];"+send,ctx);
 return {ctx,fields,closed:()=>closed};
}
test('failed send retains draft, displays error, and allows retry',async()=>{
 const f=fixture(async()=>{throw new Error('Connection interrupted')});
 await vm.runInContext('doSend()',f.ctx);
 assert.equal(f.closed(),0);assert.equal(f.fields['c-body'].value,'Keep my draft');
 assert.match(f.fields['c-err'].textContent,/Connection interrupted/);
 assert.equal(f.fields['send-mail'].disabled,false);
});
test('double click sends once with original folder and reply UID',async()=>{
 let finish,calls=0;
 const f=fixture((url,opts)=>{calls++;assert.equal(url,'/api/email/send?folder=Projects%20%26%20receipts');assert.equal(opts.body.get('uid'),'17');return new Promise(r=>finish=r);});
 const pending=vm.runInContext('doSend()',f.ctx);
 await vm.runInContext('doSend()',f.ctx);
 assert.equal(calls,1);assert.equal(f.fields['send-mail'].disabled,true);
 finish({ok:true});await pending;
 assert.equal(f.closed(),1);assert.equal(f.fields['send-mail'].disabled,false);
});
function replyFixture(){
 const fields=Object.fromEntries(['c-body','c-to','c-subject','c-err','compose-title','overlay'].map(id=>[id,{value:'',focus(){},classList:{add(){},remove(){}}}]));
 let resolve,reject;
 function fn(name){const start=html.search(new RegExp('(?:async )?function '+name+'\\('));return html.slice(start,html.indexOf('\n}',start)+2);}
 const ctx=vm.createContext({document:{getElementById:id=>fields[id]},reSubject:s=>s,looksHTML:()=>false,emailPlain:()=>'',assist:()=>new Promise((r,j)=>{resolve=r;reject=j}),renderComposeAtts(){},publishMailContext(){},PrismModal:{confirm:async()=>true}});
 vm.runInContext("let composeGeneration=0,composeAtts=[],composeSnapshot='',folder='INBOX',replyFolder='',replyUid=0,sendingMail=false,current={uid:1,from:'old@example.org',subject:'Old',date:'2026-10-15T11:41:59Z',body:'Original message'};"+['openCompose','closeCompose','quotedReply','aiReply'].map(fn).join('\n'),ctx);
 return {fields,run:s=>vm.runInContext(s,ctx),resolve:s=>resolve(s),reject:e=>reject(e)};
}
for(const failed of [false,true])test(`late AI ${failed?'failure':'reply'} cannot alter a new draft`,async()=>{
 const f=replyFixture();const pending=f.run('aiReply()');await f.run('closeCompose(true)');f.run("openCompose('New','new@example.org','New','New body')");
 if(failed)f.reject(Error('offline'));else f.resolve('Old AI reply');await pending;
 assert.equal(f.fields['c-body'].value,'New body');assert.equal(f.fields['c-err'].textContent,'');
});
test('AI completion preserves manual edits in the same draft',async()=>{
 const f=replyFixture();const pending=f.run('aiReply()');f.fields['c-body'].value='My own answer';f.resolve('Generated answer');await pending;assert.equal(f.fields['c-body'].value,'My own answer');
});
test('AI completion fills its still active untouched draft',async()=>{
 const f=replyFixture();const pending=f.run('aiReply()');f.resolve('Generated answer');await pending;assert.match(f.fields['c-body'].value,/^Generated answer\n\nOn /);assert.match(f.fields['c-body'].value,/> Original message/);
});

function openFixture(api=async()=>({message:{uid:17,from:'sender@example.org',subject:'Question',body:'Original line\nSecond line',date:'2026-10-15T06:28:32Z'}})){
 const classes=new Set(),fields=Object.fromEntries(['c-body','c-to','c-subject','c-err','compose-title'].map(id=>[id,{value:'',focus(){},setSelectionRange(start,end){this.selectionStart=start;this.selectionEnd=end;}}]));
 fields.overlay={classList:{contains:s=>classes.has(s),add:s=>classes.add(s),remove:s=>classes.delete(s)}};
 const ctx=vm.createContext({Date,document:{getElementById:id=>fields[id]},PrismAPI:{api},renderComposeAtts(){},publishMailContext(){},looksHTML:()=>false});
 function fn(name){const start=html.search(new RegExp('(?:async )?function '+name+'\\('));return html.slice(start,html.indexOf('\n}',start)+2);}
 vm.runInContext("let composeGeneration=0,composeAtts=[],composeSnapshot='',folder='Other folder',replyFolder='',replyUid=0,sendingMail=false;"+
  "const reSubject=s=>/^re:/i.test(s)?s:'Re: '+s;\n"+['openCompose','quotedReply','openMailComposer'].map(fn).join('\n')+"\nfunction compose(){replyUid=0;openCompose('New message','','','');}",ctx);
 return {fields,classes,run:s=>vm.runInContext(s,ctx),open:(args)=>ctx.openMailComposer(args,Date.now()+30000)};
}
test('agent opens a quoted reply in the original folder without sending',async()=>{
 let url;const f=openFixture(async u=>{url=u;return {message:{uid:17,from:'sender@example.org',subject:'Question',date:'2026-10-15T06:28:32Z',body:'Original line\nSecond line'}};});
 await f.open({action:'open',app:'email',uid:17,folder:'Projects & receipts'});
 assert.equal(url,'/api/email/read?folder=Projects%20%26%20receipts&uid=17');
 assert.ok(f.classes.has('open'));assert.equal(f.fields['c-to'].value,'sender@example.org');
 assert.equal(f.fields['c-subject'].value,'Re: Question');assert.match(f.fields['c-body'].value,/> Original line\n> Second line$/);
 assert.equal(f.fields['c-body'].selectionStart,0);assert.equal(f.run('replyFolder'),'Projects & receipts');assert.equal(f.run('replyUid'),17);
});
test('agent open creates a blank new draft and refuses to replace it',async()=>{
 const f=openFixture(()=>{throw Error('A new draft must not read mail');});
 await f.open({action:'open',app:'email'});f.fields['c-body'].value='My draft';
 await assert.rejects(f.open({action:'open',app:'email',uid:17}),/another draft/);
 assert.equal(f.fields['c-body'].value,'My draft');assert.equal(f.run('replyUid'),0);
});
test('late open cannot replace a composer opened while the original loads',async()=>{
 let finish;const f=openFixture(()=>new Promise(r=>finish=r));
 const pending=f.open({action:'open',app:'email',uid:17});
 f.run("compose()");f.fields['c-body'].value='Started while loading';
 finish({message:{uid:17,body:'Old original'}});
 await assert.rejects(pending,/composer changed/);assert.equal(f.fields['c-body'].value,'Started while loading');
});
test('expired open and malformed UID do not fetch or create a draft',async()=>{
 const f=openFixture(()=>{throw Error('Must not fetch');});
 await assert.rejects(f.open({action:'open',app:'email',uid:'17'}),/valid numeric/);
 await assert.rejects(f.run("openMailComposer({app:'email',uid:17},0)"),/expired/);
 assert.equal(f.classes.has('open'),false);
});
