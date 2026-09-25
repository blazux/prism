const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../app.js'),'utf8');
function fn(name,next){return source.slice(source.indexOf(`function ${name}(`),source.indexOf(`function ${next}(`));}
test('late events from an old workspace socket cannot overwrite the current conversation',()=>{
 const sockets=[],timers=[],seen=[];
 const c={ws:null,currentSessionID:'first',location:{protocol:'https:',host:'fixture'},WebSocket:class {constructor(){sockets.push(this)}},setTimeout:f=>timers.push(f),setContainerBadge:()=>{},handleServerMsg:m=>seen.push(m),console};
 vm.createContext(c);vm.runInContext(fn('connect','send'),c);
 c.connect();const old=sockets[0];old.onclose();c.currentSessionID='second';c.connect();
 old.onmessage({data:'{"type":"stream","content":"old"}'});timers[0]();
 sockets[1].onmessage({data:'{"type":"stream","content":"new"}'});
 assert.equal(sockets.length,2);assert.deepEqual(JSON.parse(JSON.stringify(seen)),[{type:'stream',content:'new'}]);
});
test('run replay restores baseline, current user message and partial output in order',()=>{
 const seen=[];
 const start=source.indexOf('function handleServerMsg('),end=source.indexOf("    case 'editor_request':",start);
 const c={clearChat:()=>seen.push('clear'),restoreChatHistory:m=>seen.push(m),appendUserMessage:m=>seen.push(m),appendStream:m=>seen.push(m),setStreaming:v=>seen.push(v)};
 vm.createContext(c);
 // Use the actual replay branches, with minimal history/stream renderers.
 vm.runInContext(source.slice(start,end)+"case 'chat_history': restoreChatHistory(msg.messages); break; case 'stream': appendStream(msg.content); break; }}",c);
 c.handleServerMsg({type:'run_replay',history:{type:'chat_history',messages:'previous'},events:[{type:'run_user',content:'request'},{type:'stream',content:'partial'}],status:'running'});
 assert.deepEqual(seen,['clear','previous','request','partial',true]);
});
test('ending an assistant text segment keeps Stop available while tools run',()=>{
 const button={classList:{add(){},remove(){}}};
 const c={activeRunStatus:'running',document:{getElementById:()=>button},cancelChat(){},sendChat(){},currentAssistantEl:null,chatWasAtBottom:()=>false};
 vm.createContext(c);vm.runInContext(fn('setStreaming','fmtTime')+fn('finalizeStream','appendProgress'),c);
 c.finalizeStream();assert.equal(c.isStreaming,true);assert.equal(button.onclick,c.cancelChat);
 c.activeRunStatus='completed';c.finalizeStream();assert.equal(c.isStreaming,false);assert.equal(button.onclick,c.sendChat);
});
