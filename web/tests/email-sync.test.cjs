const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm');
const fs=require('node:fs'),path=require('node:path');
const sync=fs.readFileSync(path.join(__dirname,'../apps/email-sync.js'),'utf8');
const selection=fs.readFileSync(path.join(__dirname,'../apps/email-selection.js'),'utf8');
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject};};
const status=(folder='INBOX',account='A',validity=1,extra={})=>({folder,account,uid_validity:validity,uid_next:8,messages:1,unread:1,highest_modseq:'1',...extra});
function fixture(){
 const state={account:'A',subject:'Original message',heldStatus:null,heldList:null,invalidations:0,renders:[]};
 const list={scrollTop:0,hasRows:true,setAttribute(){},querySelector(){return this.hasRows?{}:null},querySelectorAll(){return[]}};
 const reading={innerHTML:'Original reader'};
 const elements={mlist:list,reading,mail:{classList:{remove(){}}},'older-mail':{},'newer-mail':{},'mail-refresh-status':{classList:{toggle(){}}}};
 let ctx;
 const page=()=>({account:state.account,messages:[{uid:7,subject:state.subject,seen:false}],total:1,unread_count:1,folder_total:1,next_offset:null});
 ctx=vm.createContext({window:{PrismMailSelection:{clear(){},invalidate(){state.invalidations++;}}},document:{getElementById:id=>elements[id]||null,querySelector:()=>null,activeElement:null},
  PrismAPI:{api:async url=>url.startsWith('/api/email/status')?(state.heldStatus?.promise||status()):url.startsWith('/api/email/list')?(state.heldList?.promise||page()):{},toast(){}},
  inboxLimit:()=>30,setList(){list.hasRows=false;},renderList(){list.hasRows=true;state.renders.push(vm.runInContext('current?.subject||null',ctx));},updateMailHeading(){},draftIsOpen:()=>false,publishMailContext(){},emptyReading:()=>'',errBox:message=>'Error: '+message,setTimeout,clearTimeout});
 vm.runInContext("let folder='INBOX',mailOffset=0,mailLoadGeneration=0,unreadOnly=false,searchQuery='',readingGeneration=0,current=null,cfg={},messages=[],mailTags={};"+sync,ctx);
 const run=source=>vm.runInContext(source,ctx);
 const load=value=>{ctx.fixtureStatus=value;return run("requestMailPage('',true,{preserveReading:true,status:fixtureStatus})");};
 const reader=(name='Original reader')=>{ctx.readerName=name;run('current={uid:7,subject:readerName,seen:false};messages=[current]');reading.innerHTML=name;list.hasRows=true;};
 return {ctx,state,list,reading,run,load,reader,page};
}
test('UIDVALIDITY reset removes old UID views before the replacement page arrives',async()=>{
 const f=fixture();await f.load(status());f.reader();f.state.subject='Replacement UID7';f.state.heldList=deferred();
 const loading=f.load(status('INBOX','A',2));
 assert.equal(f.run('current'),null);assert.equal(f.reading.innerHTML,'');assert.equal(f.list.hasRows,false);
 assert.equal(f.state.invalidations,1);assert.equal(f.run('messages.length'),0);
 f.state.heldList.resolve(f.page());assert.equal(await loading,true);
 assert.equal(f.run('messages[0].subject'),'Replacement UID7');assert.equal(f.state.renders.at(-1),null);
});
test('new arrivals and modification stamps preserve the current reader and selection',async()=>{
 const f=fixture();await f.load(status());f.reader();f.state.subject='New envelope';
 assert.equal(await f.load(status('INBOX','A',1,{uid_next:99,highest_modseq:'50',messages:20})),true);
 assert.equal(f.run('current.subject'),'Original reader');assert.equal(f.reading.innerHTML,'Original reader');assert.equal(f.state.invalidations,0);
});
test('failed replacement fetch cannot restore views whose UIDs became invalid',async()=>{
 const f=fixture();await f.load(status());f.reader();f.state.heldList=deferred();
 const loading=f.load(status('INBOX','A',2));f.state.heldList.reject(new Error('Mailbox temporarily unavailable'));
 assert.equal(await loading,false);assert.equal(f.run('current'),null);assert.equal(f.run('messages.length'),0);assert.equal(f.state.invalidations,1);
});
test('UID validity is remembered separately for each folder and account',async()=>{
 const f=fixture();await f.load(status());
 for(const [folder,account,validity] of [['Projects','A',22],['INBOX','B',33],['INBOX','A',1]]){
  f.ctx.nextFolder=folder;f.ctx.nextAccount=account;f.run('folder=nextFolder;mailAccount=nextAccount');f.state.account=account;f.reader(folder+'-'+account);
  assert.equal(await f.load(status(folder,account,validity)),true);assert.equal(f.run('current.subject'),folder+'-'+account);
 }
 assert.equal(f.state.invalidations,0);
});
test('a stale STATUS response cannot clear a newer reader or poison remembered validity',async()=>{
 const f=fixture();await f.load(status());f.reader();f.state.heldStatus=deferred();
 const stale=f.run("requestMailPage('',true,{preserveReading:true})");
 f.run("folder='Projects'");f.reader('Projects reader');await f.load(status('Projects','A',4));
 f.state.heldStatus.resolve(status('INBOX','A',2));assert.equal(await stale,false);
 assert.equal(f.run('current.subject'),'Projects reader');assert.equal(f.state.invalidations,0);
 f.run("folder='INBOX'");f.reader('Old INBOX reader');await f.load(status('INBOX','A',2));
 assert.equal(f.run('current'),null);assert.equal(f.state.invalidations,1);
});
test('a STATUS for another folder cannot invalidate the active UID namespace',async()=>{
 const f=fixture();await f.load(status());f.reader();await f.load(status('Projects','A',2));
 assert.equal(f.run('current.subject'),'Original reader');assert.equal(f.state.invalidations,0);
 await f.load(status('INBOX','A',2));assert.equal(f.run('current'),null);assert.equal(f.state.invalidations,1);
});
function batchFixture(){
 const pending=[],calls=[];
 const ctx=vm.createContext({window:{},document:{querySelector:()=>null,createElement:()=>({setAttribute(){}})},folder:'INBOX',mailAccount:'A',current:null,readingGeneration:0,resetMailReading(){ctx.current=null;ctx.readingGeneration++;},
  mailTool:(_,args)=>{calls.push(args.uid);const d=deferred();pending.push(d);return d.promise;},notifyBadge(){},refreshMail:async()=>{},PrismAPI:{toast(){}}});
 vm.runInContext(selection,ctx);const api=ctx.window.PrismMailSelection;
 const items=Array.from({length:6},(_,i)=>({uid:i+1,subject:'Message '+(i+1)}));api.render(items,{account:'A'});
 for(const item of items){const checkbox=api.checkbox(item);checkbox.checked=true;checkbox.onchange();}
 return {api,pending,calls,ctx};
}
test('UID invalidation stops queued batch requests while its three in-flight requests finish',async()=>{
 const f=batchFixture(),job=f.api.run('archive');assert.deepEqual(f.calls,[1,2,3]);
 f.api.invalidate();const replacement={uid:1,subject:'Replacement using the old UID'};f.ctx.current=replacement;
 f.pending.splice(0).forEach(item=>item.resolve());const result=await job;
 assert.deepEqual(f.calls,[1,2,3]);assert.equal(result.confirmed.length,3);assert.deepEqual(Array.from(result.unprocessed,item=>item.uid),[4,5,6]);
 assert.equal(f.ctx.current,replacement,'late completion from an invalidated batch must not clear a replacement reader');
});
test('clearing a selection during ordinary navigation does not cancel its captured batch',async()=>{
 const f=batchFixture(),job=f.api.run('archive');f.api.clear();
 while(f.pending.length){f.pending.splice(0).forEach(item=>item.resolve());await new Promise(resolve=>setImmediate(resolve));}
 const result=await job;assert.deepEqual(f.calls,[1,2,3,4,5,6]);assert.equal(result.confirmed.length,6);assert.equal(result.unprocessed.length,0);
});
test('UID invalidation prevents old visible rows from being selected for a new batch',async()=>{
 const f=batchFixture();f.api.invalidate();
 const stale=f.api.checkbox({uid:1,subject:'Old UID'});stale.checked=true;stale.onchange();
 assert.equal(await f.api.run('archive'),undefined);assert.deepEqual(f.calls,[]);
});
test('an invalidated Move snapshot cannot rearm a batch for the old UIDs',async()=>{
 const f=batchFixture();
 const moving=f.api.run('move','Projects',{folder:'INBOX',account:'A',action:'move',target:'Projects',items:[{uid:1,subject:'Old UID'}],invalidated:true});
 assert.deepEqual(f.calls,[]);assert.equal(await moving,undefined);
});
