const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const app=fs.readFileSync(path.join(__dirname,'../app.js'),'utf8'),widget=fs.readFileSync(path.join(__dirname,'../prism-widget.js'),'utf8');
function fn(name){const start=app.indexOf('function '+name+'(');assert.ok(start>=0,name);return app.slice(start,app.indexOf('\n}',start)+2);}
function fixture(){
 const listeners=new Set(),timers=new Map(),sent=[],bubbles=[],errors=[],notices=[];
 const input={value:'My unsent draft'},images=[{name:'image.png',data:'image'}],files=[{name:'report',path:'report.txt'}];let bridge,nextTimer=0;
 const parent={postMessage:data=>bridge({origin:'https://prism.test',source:widgetWindow,data})};
 const widgetWindow={parent,addEventListener:(type,listener)=>listeners.add(listener),removeEventListener:(type,listener)=>listeners.delete(listener),postMessage:data=>{for(const listener of [...listeners])listener({data,source:parent})}};
 const wctx=vm.createContext({window:widgetWindow,document:{addEventListener(){}},setTimeout:callback=>{timers.set(++nextTimer,callback);return nextTimer},clearTimeout:id=>timers.delete(id)});vm.runInContext(widget,wctx);
 const c={window:{addEventListener:(type,listener)=>bridge=listener},location:{origin:'https://prism.test'},WIDGET_HOSTING:null,currentSessionID:'board',isStreaming:false,pendingChats:new Map(),pendingImages:images,pendingFiles:files,document:{getElementById:()=>input},WebSocket:{OPEN:1},ws:{readyState:1,send:raw=>sent.push(JSON.parse(raw))},crypto:require('node:crypto').webcrypto,getDisabledTools:()=>[],renderChatStatus(){},autoResizeTextarea(){},renderPreviews(){},appendUserMessage:(...args)=>bubbles.push(args),appendError:message=>errors.push(message),showToast:notice=>notices.push(notice),chatOpen:true,toggleChat(){}};
 vm.createContext(c);vm.runInContext(['send','replyToWidgetChat','sendChat','receiveChatReceipt'].map(fn).join('\n'),c);
 const start=app.indexOf("window.addEventListener('message', e => {",app.indexOf('// Listen for postMessage calls'));
 vm.runInContext(app.slice(start,app.indexOf('// ─── Chat drawer resize',start)),c);
 return {c,widget:widgetWindow,parent,input,sent,bubbles,errors,notices,timers,listeners,run:s=>vm.runInContext(s,c)};
}
test('prismChat waits for a correlated server receipt and preserves the parent draft and attachments',async()=>{
 const f=fixture();let resolved=false;const request=f.widget.prismChat('My unsent draft');request.then(()=>resolved=true);await Promise.resolve();
 assert.equal(resolved,false);assert.equal(f.sent.length,1);assert.equal(f.sent[0].content,'My unsent draft');assert.equal(f.sent[0].images,undefined);assert.equal(f.sent[0].files,undefined);assert.equal(f.input.value,'My unsent draft');
 const id=f.c.pendingChats.get('board').widget.id;
 for(const listener of f.listeners){listener({source:{},data:{type:'prism-chat-response',id,status:'accepted'}});listener({source:f.parent,data:{type:'prism-chat-response',id:'other',status:'accepted'}})}
 f.c.receiveChatReceipt({requestId:'other',status:'accepted'});await Promise.resolve();assert.equal(resolved,false);
 f.c.receiveChatReceipt({requestId:f.sent[0].requestId,status:'accepted'});assert.equal((await request).status,'accepted');assert.equal(f.input.value,'My unsent draft');assert.equal(f.c.pendingImages.length,1);assert.equal(f.c.pendingFiles.length,1);assert.equal(f.bubbles.length,1);assert.equal(f.timers.size,0);assert.equal(f.listeners.size,0);
 f.c.receiveChatReceipt({requestId:f.sent[0].requestId,status:'completed',replayed:true});assert.equal(f.bubbles.length,1);
});
test('busy, pending, disconnected and rejected widget sends reject explicitly without changing the draft',async()=>{
 for(const state of ['streaming','pending','disconnected','rejected']){
  const f=fixture();if(state==='streaming')f.c.isStreaming=true;if(state==='pending')f.c.pendingChats.set('board',{payload:{requestId:'older'}});if(state==='disconnected')f.c.ws.readyState=3;
  const request=f.widget.prismChat('New widget message');const rejected=assert.rejects(request,/busy|unavailable|did not accept/);
  if(state==='rejected')f.c.receiveChatReceipt({requestId:f.sent[0].requestId,status:'rejected'});await rejected;
  assert.equal(f.input.value,'My unsent draft');assert.equal(f.c.pendingImages.length,1);assert.equal(f.c.pendingFiles.length,1);assert.equal(f.timers.size,0);
  if(state!=='rejected')assert.equal(f.sent.length,0);
 }
});
test('unconfirmed acceptance and timeout reject without sending a second request',async()=>{
 for(const event of ['unconfirmed','timeout']){
  const f=fixture(),request=f.widget.prismChat('Analyse');const rejected=assert.rejects(request,/Check the chat before sending again/);
  if(event==='timeout')[...f.timers.values()][0]();else f.c.receiveChatReceipt({requestId:f.sent[0].requestId,status:'unknown'});
  await rejected;assert.equal(f.sent.length,1);assert.equal(f.c.pendingChats.size,1);assert.equal(f.input.value,'My unsent draft');
  f.c.receiveChatReceipt({requestId:f.sent[0].requestId,status:'accepted'});assert.equal(f.bubbles.length,1);assert.equal(f.sent.length,1);
 }
});
test('legacy one-way sendChat messages still send and report busy errors through the dashboard',()=>{
 const f=fixture();f.c.sendChat({text:'Legacy message',widget:{source:{postMessage(){throw Error('No response expected')}},origin:'https://prism.test',id:null}});assert.equal(f.sent.length,1);assert.equal(f.input.value,'My unsent draft');
 f.c.sendChat({text:'Second message',widget:{source:{},origin:'https://prism.test',id:null}});assert.equal(f.sent.length,1);assert.match(f.notices[0].message,/busy/);
});
