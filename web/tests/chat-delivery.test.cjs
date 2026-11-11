const {test}=require('node:test');
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../app.js'),'utf8');
const send=source.slice(source.indexOf('function send(obj) {')).split('\n}\n')[0]+'\n}';
const sendChat=source.slice(source.indexOf('function sendChat() {'),source.indexOf('\nwindow.sendChat = sendChat'));

function harness(state,throws=false){
 const input={value:'Please analyse this image'},messages=[],toasts=[],sent=[];
 const c={currentSessionID:'fixture',pendingChats:new Map(),crypto:require('node:crypto').webcrypto,renderChatStatus:()=>{},ws:state===null?null:{readyState:state,send:raw=>{if(throws)throw new Error('socket closed');sent.push(JSON.parse(raw))}},
  WebSocket:{OPEN:1},document:{getElementById:()=>input},pendingImages:[{data:'image-data',name:'diagram.png'}],pendingFiles:[{name:'report.txt',path:'uploads/report.txt'}],isStreaming:false,
  appendUserMessage:(...args)=>messages.push(args),autoResizeTextarea:()=>{},renderPreviews:()=>{},getDisabledTools:()=>[],setStreaming:value=>{c.isStreaming=value},showToast:t=>toasts.push(t)};
 vm.createContext(c);vm.runInContext(send+'\n'+sendChat+'\n'+source.match(/function receiveChatReceipt\([^]*?^}/m)[0],c);
 return {c,input,messages,toasts,sent};
}

test('a disconnected/connecting/failed socket keeps the draft and every attachment',()=>{
 for(const [state,throws] of [[null,false],[0,false],[2,false],[3,false],[1,true]]){
  const h=harness(state,throws);h.c.sendChat();
  assert.equal(h.input.value,'Please analyse this image');assert.equal(h.c.pendingImages.length,1);assert.equal(h.c.pendingFiles.length,1);
  assert.equal(h.messages.length,0);assert.equal(h.sent.length,0);assert.equal(h.c.isStreaming,false);assert.equal(h.toasts.length,1);
 }
});

test('manual retry sends once and keeps the draft until the server receipt',()=>{
 const h=harness(3);h.c.sendChat();h.c.ws.readyState=1;h.c.sendChat();h.c.sendChat();
 assert.equal(h.sent.length,1);assert.equal(h.messages.length,0);assert.equal(h.c.pendingChats.size,1);assert.equal(h.sent[0].content,'Please analyse this image');
 assert.deepEqual(h.sent[0].images,['image-data']);assert.deepEqual(h.sent[0].imageNames,['diagram.png']);assert.equal(h.sent[0].files[0].path,'uploads/report.txt');
 assert.equal(h.input.value,'Please analyse this image');assert.equal(h.c.pendingImages.length,1);assert.equal(h.c.pendingFiles.length,1);assert.equal(h.c.isStreaming,false);
});

test('only the matching receipt clears a draft and late receipts cannot duplicate its bubble',()=>{
 const h=harness(1);h.c.sendChat();const id=h.sent[0].requestId;
 h.c.receiveChatReceipt({requestId:'unrelated',status:'accepted'});assert.equal(h.input.value,'Please analyse this image');
 h.c.receiveChatReceipt({requestId:id,status:'unknown'});assert.equal(h.c.pendingChats.get('fixture').uncertain,true);assert.equal(h.messages.length,0);
 h.c.receiveChatReceipt({requestId:id,status:'accepted',replayed:false});h.c.receiveChatReceipt({requestId:id,status:'completed',replayed:true});
 assert.equal(h.input.value,'');assert.equal(h.c.pendingImages.length,0);assert.equal(h.messages.length,1);
});

test('a receipt keeps an upload that finished after the request was sent',()=>{
 const h=harness(1);h.c.sendChat();const later={name:'later.txt',path:'uploads/later.txt'};h.c.pendingFiles.push(later);
 h.c.receiveChatReceipt({requestId:h.sent[0].requestId,status:'accepted',replayed:false});
 assert.equal(h.c.pendingFiles.length,1);assert.equal(h.c.pendingFiles[0],later);
});
