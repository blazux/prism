const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {readFileSync}=require('node:fs');
const path=require('node:path');
const html=readFileSync(path.join(__dirname,'../apps/email.html'),'utf8');
const app=readFileSync(path.join(__dirname,'../app.js'),'utf8');
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject};};
function mailFixture(){
 const pending=deferred(),notifications=[],toasts=[];
 const ctx=vm.createContext({parent:{postMessage:m=>notifications.push(m)},renderList:()=>{},mailURL:x=>x,PrismAPI:{api:()=>pending.promise,body:()=>({}),toast:m=>toasts.push(m)}});
 vm.runInContext("let folder='INBOX',messages=[{uid:7,seen:false}],current=messages[0];"+html.slice(html.indexOf('function notifyBadge()'),html.indexOf('function stripHtml(')),ctx);
 return {ctx,pending,notifications,toasts};
}
test('badge refresh waits for IMAP confirmation, including after switching folders',async()=>{
 const f=mailFixture();const done=vm.runInContext('markSeen(7,true)',f.ctx);
 assert.equal(vm.runInContext('current.seen',f.ctx),true);
 assert.equal(f.notifications.length,0);
 vm.runInContext("folder='Archive'",f.ctx);
 f.pending.resolve({});await done;
 assert.equal(f.notifications.length,1);
 assert.equal(f.notifications[0].type,'mail-unread-changed');
});
test('failed read update restores unread state and refreshes server count',async()=>{
 const f=mailFixture();const done=vm.runInContext('markSeen(7,true)',f.ctx);
 f.pending.reject(new Error('IMAP refused'));await done;
 assert.equal(vm.runInContext('current.seen',f.ctx),false);
 assert.equal(f.notifications.length,1);assert.equal(f.toasts.length,1);
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
