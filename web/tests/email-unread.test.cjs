const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {readFileSync}=require('node:fs');
const path=require('node:path');
const html=readFileSync(path.join(__dirname,'../apps/email.html'),'utf8');
const app=readFileSync(path.join(__dirname,'../app.js'),'utf8');
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject};};
function mailFixture(){
 const pending=deferred(),notifications=[],toasts=[],requests=[],refreshes=[];
 const ctx=vm.createContext({parent:{postMessage:m=>notifications.push(m)},renderList:()=>{},queueMailRefresh:()=>refreshes.push(true),PrismAPI:{api:(url,options)=>{requests.push({url,...options});return pending.promise;},body:(method,body)=>({method,body:JSON.stringify(body)}),toast:m=>toasts.push(m)}});
 vm.runInContext("let folder='INBOX',mailAccount='mailbox-A',mailPagination={unread_count:3,total:8},unreadOnly=false,messages=[{uid:7,seen:false}],current=messages[0];"+html.slice(html.indexOf('function notifyBadge()'),html.indexOf('function stripHtml(')),ctx);
 return {ctx,pending,notifications,toasts,requests,refreshes};
}
test('badge refresh waits for IMAP confirmation, including after switching folders',async()=>{
 const f=mailFixture();const done=vm.runInContext('markSeen(7,true)',f.ctx);
 assert.equal(vm.runInContext('current.seen',f.ctx),true);
 assert.equal(vm.runInContext('mailPagination.unread_count',f.ctx),2);
 assert.equal(f.requests[0].url,'/api/email/markseen?folder=INBOX');
 assert.deepEqual(JSON.parse(f.requests[0].body),{uid:7,seen:true,account:'mailbox-A'});
 assert.equal(f.notifications.length,0);
 vm.runInContext("folder='Archive'",f.ctx);
 f.pending.resolve({});await done;
 assert.equal(f.notifications.length,1);
 assert.equal(f.notifications[0].type,'mail-unread-changed');
 assert.equal(f.refreshes.length,0,'completion must not refresh the new folder');
});
test('failed read update restores unread state and refreshes server count',async()=>{
 const f=mailFixture();const done=vm.runInContext('markSeen(7,true)',f.ctx);
 f.pending.reject(new Error('IMAP refused'));await done;
 assert.equal(vm.runInContext('current.seen',f.ctx),false);
 assert.equal(vm.runInContext('mailPagination.unread_count',f.ctx),3);
 assert.equal(f.notifications.length,1);assert.equal(f.toasts.length,1);
 assert.equal(f.refreshes.length,0);
});
test('confirmed read updates refresh only the current mailbox after server confirmation',async()=>{
 const f=mailFixture();vm.runInContext('unreadOnly=true;mailPagination.total=3',f.ctx);
 const done=vm.runInContext('markSeen(7,true)',f.ctx);
 assert.equal(vm.runInContext('mailPagination.total',f.ctx),2);
 assert.equal(f.refreshes.length,0);
 f.pending.resolve({});await done;
 assert.equal(f.refreshes.length,1);assert.equal(f.notifications.length,1);
 assert.equal(vm.runInContext('current.uid',f.ctx),7,'the current reading pane survives a read flag change');
});
test('late read failure cannot change a replacement mailbox with the same message UID',async()=>{
 const f=mailFixture();const done=vm.runInContext('markSeen(7,true)',f.ctx);
 vm.runInContext("mailAccount='mailbox-B';messages=[{uid:7,seen:true}];current=messages[0];mailPagination={unread_count:12,total:20}",f.ctx);
 f.pending.reject(new Error('Original mailbox disconnected'));await done;
 assert.equal(vm.runInContext('current.seen',f.ctx),true);
 assert.equal(vm.runInContext('mailPagination.unread_count',f.ctx),12);
 assert.equal(f.refreshes.length,0);assert.equal(f.notifications.length,1);
});
test('late read confirmation cannot refresh a replacement mailbox',async()=>{
 const f=mailFixture();const done=vm.runInContext('markSeen(7,true)',f.ctx);
 vm.runInContext("mailAccount='mailbox-B'",f.ctx);
 f.pending.resolve({});await done;
 assert.equal(f.refreshes.length,0);assert.equal(f.notifications.length,1);
});
test('failed redundant read update keeps an already read message and its counts unchanged',async()=>{
 const f=mailFixture();vm.runInContext('current.seen=true',f.ctx);
 const done=vm.runInContext('markSeen(7,true)',f.ctx);
 f.pending.reject(Object.assign(new Error('Mailbox temporarily unavailable'),{status:503}));await done;
 assert.equal(vm.runInContext('current.seen',f.ctx),true);
 assert.equal(vm.runInContext('mailPagination.unread_count',f.ctx),3);
 assert.equal(vm.runInContext('mailPagination.total',f.ctx),8);
 assert.equal(f.refreshes.length,0);assert.equal(f.notifications.length,1);assert.equal(f.toasts.length,1);
});
test('late badge poll cannot restore a stale unread count',async()=>{
 const requests=[];let badge={textContent:'1',remove(){badge=null;}};
 const icon={querySelector:()=>badge,appendChild:b=>{badge=b;}};
 const ctx=vm.createContext({DISABLED_APPS:new Set(),fetch:()=>{const d=deferred();requests.push(d);return d.promise;},document:{querySelector:()=>icon,createElement:()=>({remove(){badge=null;}})}});
 vm.runInContext(app.slice(app.indexOf('let mailBadgeGeneration'),app.indexOf('function renderBoardList(')),ctx);
 const old=vm.runInContext('refreshMailBadge()',ctx);
 const fresh=vm.runInContext('refreshMailBadge()',ctx);
 requests[1].resolve({ok:true,json:async()=>({count:0})});await fresh;assert.equal(badge,null);
 requests[0].resolve({ok:true,json:async()=>({count:1})});await old;assert.equal(badge,null);
});
