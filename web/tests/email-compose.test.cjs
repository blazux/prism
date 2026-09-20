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
 const ctx=vm.createContext({document:{getElementById:id=>fields[id]},reSubject:s=>s,emailPlain:()=>'',assist:()=>new Promise((r,j)=>{resolve=r;reject=j}),renderComposeAtts(){},publishMailContext(){},PrismModal:{confirm:async()=>true}});
 vm.runInContext("let composeGeneration=0,composeAtts=[],composeSnapshot='',folder='INBOX',replyFolder='',replyUid=0,sendingMail=false,current={uid:1,from:'old@example.org',subject:'Old'};"+['openCompose','closeCompose','aiReply'].map(fn).join('\n'),ctx);
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
 const f=replyFixture();const pending=f.run('aiReply()');f.resolve('Generated answer');await pending;assert.equal(f.fields['c-body'].value,'Generated answer');
});
