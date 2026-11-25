const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'../app.js'),'utf8');
function fn(name,next){const prefix=name==='connect'?'async ':'';return source.slice(source.indexOf(`${prefix}function ${name}(`),source.indexOf(`function ${next}(`));}
test('late events from an old workspace socket cannot overwrite the current conversation',async()=>{
 const sockets=[],timers=[],seen=[];
 const c={ws:null,currentSessionID:'first',location:{protocol:'https:',host:'fixture'},WebSocket:class {constructor(){sockets.push(this)}},setTimeout:f=>timers.push(f),setContainerBadge:()=>{},handleServerMsg:m=>seen.push(m),console,renderChatStatus:()=>{}};
 c.PrismPreferences={ensureReady:async()=>true};c.connectionAttempt=0;c.approvalInitialized=true;
 vm.createContext(c);vm.runInContext(fn('connect','send'),c);
 await c.connect();const old=sockets[0];old.onclose();c.currentSessionID='second';await c.connect();
 old.onmessage({data:'{"type":"stream","content":"old"}'});timers[0]();
 sockets[1].onmessage({data:'{"type":"stream","content":"new"}'});
 assert.equal(sockets.length,2);assert.deepEqual(JSON.parse(JSON.stringify(seen)),[{type:'stream',content:'new'}]);
});
test('run replay restores baseline, current user message and partial output in order',()=>{
 const seen=[];
 const start=source.indexOf('function handleServerMsg('),end=source.indexOf("    case 'editor_request':",start);
 const c={clearChat:()=>seen.push('clear'),restoreChatHistory:m=>seen.push(m),appendUserMessage:m=>seen.push(m),appendStream:m=>seen.push(m),setStreaming:v=>seen.push(v),renderChatStatus:()=>{}};
 vm.createContext(c);
 // Use the actual replay branches, with minimal history/stream renderers.
 vm.runInContext(source.slice(start,end)+"case 'chat_history': restoreChatHistory(msg.messages); break; case 'stream': appendStream(msg.content); break; }}",c);
 c.handleServerMsg({type:'run_replay',history:{type:'chat_history',messages:'previous'},events:[{type:'run_user',content:'request'},{type:'stream',content:'partial'}],status:'running'});
 assert.deepEqual(seen,['clear','previous','request','partial',true]);
});
test('ending an assistant text segment keeps Stop available while tools run',()=>{
 const button={classList:{add(){},remove(){}}};
 const c={activeRunStatus:'running',document:{getElementById:()=>button},cancelChat(){},sendChat(){},currentAssistantEl:null,pendingAttachments:[],chatWasAtBottom:()=>false};
 vm.createContext(c);vm.runInContext(fn('setStreaming','fmtTime')+fn('finalizeStream','appendProgress'),c);
 c.finalizeStream();assert.equal(c.isStreaming,true);assert.equal(button.onclick,c.cancelChat);
 c.activeRunStatus='completed';c.finalizeStream();assert.equal(c.isStreaming,false);assert.equal(button.onclick,c.sendChat);
});
test('restored chat diagnostics stay visible as errors',()=>{
 const seen=[];
 const c={document:{getElementById:()=>({appendChild:n=>seen.push(n.className)}),createElement:()=>({})},appendError:m=>seen.push({error:m}),scrollChat:()=>{}};
 vm.createContext(c);vm.runInContext(fn('restoreChatHistory','chatAtBottom'),c);
 c.restoreChatHistory([{role:'error',content:'The model connection ended before completion.'}]);
 assert.deepEqual(seen,['chat-history-sep',{error:'The model connection ended before completion.'},'chat-history-sep']);
});
test('an older session-list response cannot restore a finished workspace badge',async()=>{
 const pending=[],rendered=[];
 const c={allSessions:[],voxWorkspaceAvailable:false,currentView:{type:'app'},fetch:async url=>{
  if(url==='/api/sessions')return new Promise(resolve=>pending.push(resolve));
  return {json:async()=>({available:false})};
 },applyDisabledApps(){},renderBoardList:rows=>rendered.push(rows),document:{}};
 vm.createContext(c);
 vm.runInContext(source.slice(source.indexOf('let sessionLoadGeneration'),source.indexOf('// ─── Workspace icons')),c);
 const older=c.loadSessions(),newer=c.loadSessions();
 pending[1]({ok:true,json:async()=>({sessions:[{id:'finance'}]})});await newer;
 pending[0]({ok:true,json:async()=>({sessions:[{id:'finance',runStatus:'running'}]})});await older;
 assert.equal(rendered.length,1);assert.equal(rendered[0][0].runStatus,undefined);
 assert.equal(c.allSessions[0].runStatus,undefined);
});
test('workspace badges only represent active agent states',()=>{
 function node(){return {dataset:{},children:[],classList:{add(){}},append(...children){this.children.push(...children)},appendChild(child){this.children.push(child)}}}
 const host=node();
 const c={document:{getElementById:()=>host,createElement:node},ASSISTANT:'assistant',currentView:{type:'app'},currentSessionID:'default',getWorkspaceIcon:()=>'',WS_ICONS:{},wsIconSvg:()=>'',selectBoard(){},editWorkspace(){}};
 vm.createContext(c);vm.runInContext(fn('renderBoardList','selectBoard'),c);
 const states=['running','waiting_for_input','waiting_for_approval','completed','failed','stopped','interrupted','idle',undefined];
 c.renderBoardList(states.map((runStatus,i)=>({id:'board-'+i,name:'Board '+i,runStatus})));
 const badges=host.children.map(item=>item.children[0].children.filter(el=>el.className==='rail-badge'));
 assert.deepEqual(badges.map(list=>list.length),[1,1,1,0,0,0,0,0,0]);
 assert.equal(badges[0][0].title,'Agent working');
 assert.equal(badges[1][0].title,'Waiting for your input');
});
test('workspace status refresh does not wait for telephony availability',async()=>{
 let finishVox;const rendered=[];
 const c={allSessions:[],voxWorkspaceAvailable:false,currentView:{type:'app'},fetch:async url=>{
  if(url==='/api/sessions')return {ok:true,json:async()=>({sessions:[{id:'finance'}]})};
  return new Promise(resolve=>finishVox=resolve);
 },applyDisabledApps(){},renderBoardList:rows=>rendered.push(rows),document:{}};
 vm.createContext(c);
 vm.runInContext(source.slice(source.indexOf('let sessionLoadGeneration'),source.indexOf('// ─── Workspace icons')),c);
 const request=c.loadSessions();await new Promise(setImmediate);
 assert.equal(rendered.length,1);assert.equal(rendered[0][0].runStatus,undefined);
 finishVox({json:async()=>({available:false})});await request;
});
