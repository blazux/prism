const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const html = fs.readFileSync(path.join(__dirname, '../apps/room.html'), 'utf8');

function roomClient({network=false} = {}) {
  const nodes = new Map();
  const element = id => {
    if (!nodes.has(id)) {
      const classes = new Set(), listeners = new Map();
      nodes.set(id, {
      hidden:true, disabled:false, innerHTML:'', textContent:'', style:{}, value:'',
      scrollHeight:0, scrollTop:0, clientHeight:0,
      classList:{contains:x=>classes.has(x),add:x=>classes.add(x),remove:x=>classes.delete(x),toggle(x,on){if(on)classes.add(x);else classes.delete(x);}},
      addEventListener:(name,fn)=>listeners.set(name,fn),dispatch:(name,event={})=>listeners.get(name)?.(event),
      querySelectorAll:()=>[],querySelector:()=>null,focus(){},
      });
    }
    return nodes.get(id);
  };
  const sockets = [], toasts = [], timers = new Map(), listeners = new Map();
  let timerID = 0, requestID = 0;
  class RoomSocket {
    constructor(url){this.url=url;this.readyState=0;this.frames=[];sockets.push(this);}
    async open(){this.readyState=1;await Promise.resolve();this.onopen?.();}
    async deliver(frame){await Promise.resolve();this.onmessage?.({data:JSON.stringify(frame)});}
    send(frame){this.frames.push(JSON.parse(frame));}
    close(){this.readyState=3;this.onclose?.();}
  }
  const context = {
    document:{getElementById:element,querySelector:()=>null,addEventListener(){}},
    navigator:{language:'fr'}, Date, Map, Set, JSON, console,
    location:{protocol:'http:',host:'fixture.test',origin:'http://fixture.test',search:''},WebSocket:RoomSocket,
    parent:{postMessage(){}},addEventListener:(name,handler)=>listeners.set(name,handler),
    crypto:{randomUUID:()=>`room-client-request-${++requestID}`},
    setTimeout:network ? fn=>{timers.set(++timerID,fn);return timerID;} : setTimeout,
    clearTimeout:network ? id=>timers.delete(id) : clearTimeout,
    PrismAPI:{toast:message=>toasts.push(message),api(){if(network)return Promise.resolve({members:[]});throw Error('Unexpected request');}},
  };
  context.window=context;
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(path.join(__dirname,'../smileys.js'),'utf8'),context);
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)];
  const script = scripts.at(-1)[1].replace(/init\(\)\s*$/, '');
  vm.runInContext(script, context);
  // This test drives the real frame handler and render functions. The browser
  // fixture checks DOM updates; here only the full-list DOM rebuild is stubbed.
  vm.runInContext('renderAll = () => {}; renderTyping = () => {}; ME = {uid:1,name:"Vincent"}', context);
  return {context,element,sockets,toasts,dispatch:(name,event)=>listeners.get(name)?.(event),read:expression=>vm.runInContext(expression,context),
    async runTimers(){const callbacks=[...timers.values()];timers.clear();for(const fn of callbacks)fn();await Promise.resolve();}};
}
const question = {id:1,groupId:1,authorId:1,authorName:'Vincent',content:'@assistant salut',isAgent:false,createdAt:'2026-10-26T23:45:00-04:00'};
const pending = {id:2,groupId:1,authorName:'Assistant',content:'',isAgent:true,status:'running',replyTo:1,createdAt:'2026-10-26T23:45:01-04:00'};

test('room smileys affect display while messages and code remain literal',()=>{
  const {context:c,read}=roomClient();
  const message={...question,content:'Salut :D :thumbsup: `:smile:`'};
  c.handleRoomEvent({type:'history',messages:[message]});
  assert.equal(read('msgById.get(1).content'),message.content);
  const html=read('rowHTML(msgById.get(1),null)');
  assert.match(html,/Salut 😃 👍 <code>:smile:<\/code>/);
});

test('started, streamed and completed frames update one response bubble',()=>{
  const {context:c,read}=roomClient();
  c.handleRoomEvent({type:'history',messages:[{...question}]});
  c.handleRoomEvent({type:'message',message:{...pending}});
  c.handleRoomEvent({type:'agent_stream',messageId:2,content:'Salut Vincent'});
  assert.equal(read('msgById.get(2).content'),'Salut Vincent');
  const done={...pending,status:'complete',content:'Salut Vincent 👋 Comment je peux t’aider ?'};
  c.handleRoomEvent({type:'message',message:done});
  c.handleRoomEvent({type:'message',message:{...done}});
  c.handleRoomEvent({type:'history',messages:[{...question},{...done}]});
  assert.equal(read('msgs.length'),2);
  assert.equal(read('msgById.size'),2);
  assert.equal(read('msgs[1].content'),done.content);
  const rendered=c.rowHTML(read('msgs[1]'),read('msgs[0]'));
  assert.equal((rendered.match(/data-id="2"/g)||[]).length,1);
  assert.equal((rendered.match(/class="quoted"/g)||[]).length,0);
  assert.ok(rendered.includes('Assistant'));
});

test('citations remain when needed for interleaved or explicit replies',()=>{
  const {context:c}=roomClient();
  c.handleRoomEvent({type:'history',messages:[{...question}]});
  const elsewhere={...question,id:3,authorName:'Other',content:'Another topic'};
  const reply={...pending,id:4,status:'complete',content:'Salut'};
  assert.equal((c.rowHTML(reply,elsewhere).match(/class="quoted"/g)||[]).length,1);
  const explicit={...question,id:5,replyTo:1,content:'Explicit human reply'};
  assert.ok(c.rowHTML(explicit,question).includes('class="quoted"'));
});

test('tool events are retained through progress, completion and history replay',()=>{
  const {context:c,read,element}=roomClient();
  c.handleRoomEvent({type:'history',messages:[{...question}]});
  c.handleRoomEvent({type:'message',message:{...pending}});
  const use={type:'tool_use',id:'t1',tool:'note',input:{action:'list',query:'<img src=x>'}};
  const result={type:'tool_result',id:'t1',output:'<script>fixture</script>',is_error:true};
  for (const event of [use,result,result]) c.handleRoomEvent({type:'agent_event',messageId:2,event});
  assert.equal(read('msgById.get(2).toolEvents.length'),2);
  const done={...pending,status:'complete',content:'Done',toolEvents:[use,result]};
  c.handleRoomEvent({type:'message',message:done});
  c.handleRoomEvent({type:'history',messages:[{...question},{...done}]});
  const rendered=c.rowHTML(read('msgById.get(2)'),question);
  assert.ok(rendered.includes('note · Échec'));
  assert.ok(rendered.includes('&lt;img')&&rendered.includes('&lt;script&gt;'));
  assert.ok(!rendered.includes('<script>'));
  c.handleRoomEvent({type:'activity',running:true,queued:1});
  assert.equal(element('room-stop').disabled,false);
  assert.equal(element('context-compact').disabled,true);
  c.handleRoomEvent({type:'activity',running:false,queued:0});
  assert.equal(element('room-stop').disabled,true);
  assert.equal(element('context-compact').disabled,false);
});

test('composer keeps text and reply until the correlated durable receipt arrives',async()=>{
  const {context:c,element,read,sockets}=roomClient({network:true});
  c.connect('1');await sockets[0].open();
  element('box').value='  Ready to send  ';c.setReply(question);c.send();
  const sent=sockets[0].frames[0];
  assert.equal(sent.content,'Ready to send');assert.equal(sent.replyTo,1);
  assert.equal(element('box').value,'  Ready to send  ');assert.equal(read('replyTo'),1);
  await sockets[0].deliver({type:'message',message:{...question,content:sent.content}});
  assert.equal(element('box').value,'  Ready to send  ');
  await sockets[0].deliver({type:'room_receipt',groupId:1,requestId:'another-request',status:'accepted'});
  assert.equal(element('box').value,'  Ready to send  ');
  await sockets[0].deliver({type:'room_receipt',groupId:1,requestId:sent.requestId,status:'accepted',message:{...question,content:sent.content}});
  assert.equal(element('box').value,'');assert.equal(read('replyTo'),null);assert.equal(read('msgs.length'),1);
  assert.equal(read('pendingMessages.size'),0);
});

test('lost receipt reconnects with the same id and keeps subsequent edits',async()=>{
  const fixture=roomClient({network:true}),{context:c,element,read,sockets}=fixture;
  c.connect('1');await sockets[0].open();element('box').value='First draft';c.send();
  const sent=sockets[0].frames[0];
  // The durable write happened, but its receipt was lost with the socket.
  await fixture.runTimers();assert.equal(sockets[0].readyState,3);
  await fixture.runTimers();await sockets[1].open();
  assert.equal(sockets[1].frames[0].requestId,sent.requestId);
  assert.equal(element('box').value,'First draft');
  element('box').value='A newer draft';element('box').dispatch('input');c.setReply({...question,id:9});
  await sockets[1].deliver({type:'room_receipt',groupId:1,requestId:sent.requestId,status:'accepted',replayed:true,message:question});
  assert.equal(element('box').value,'A newer draft');assert.equal(read('replyTo'),9);
  assert.equal(read('pendingMessages.size'),0);
});

test('uncertain database delivery retains the id while an explicit rejection permits correction',async()=>{
  const fixture=roomClient({network:true}),{context:c,element,read,sockets,toasts}=fixture;
  c.connect('1');await sockets[0].open();element('box').value='Draft';c.setReply(question);c.send();
  const sent=sockets[0].frames[0];
  await sockets[0].deliver({type:'room_receipt',groupId:1,requestId:sent.requestId,status:'unavailable'});
  assert.equal(element('box').value,'Draft');assert.equal(read('replyTo'),1);
  await fixture.runTimers();await sockets[1].open();
  assert.equal(sockets[1].frames[0].requestId,sent.requestId);
  await sockets[1].deliver({type:'room_receipt',groupId:1,requestId:sent.requestId,status:'rejected',content:'Quoted message is unavailable.'});
  assert.equal(element('box').value,'Draft');assert.equal(read('replyTo'),1);assert.equal(read('pendingMessages.size'),0);
  assert.equal(toasts.at(-1),'Quoted message is unavailable.');
  c.clearReply();c.send();assert.notEqual(sockets[1].frames.at(-1).requestId,sent.requestId);
});

test('switching rooms preserves each draft and retries only its own pending message',async()=>{
  const {context:c,element,read,sockets}=roomClient({network:true});
  c.connect('1');await sockets[0].open();element('box').value='Room one';c.setReply(question);c.send();
  const first=sockets[0].frames[0];
  c.connect('2');await sockets[1].open();
  assert.equal(element('box').value,'');assert.equal(read('replyTo'),null);assert.equal(sockets[1].frames.length,0);
  element('box').value='Room two';element('box').dispatch('input');
  c.connect('1');await sockets[2].open();
  assert.equal(element('box').value,'Room one');assert.equal(read('replyTo'),1);
  assert.equal(sockets[2].frames[0].requestId,first.requestId);
  await sockets[2].deliver({type:'room_receipt',groupId:1,requestId:first.requestId,status:'accepted',message:question});
  c.connect('2');await sockets[3].open();assert.equal(element('box').value,'Room two');
});

test('the message limit counts Unicode codepoints and preserves oversize text',async()=>{
  const {context:c,element,sockets,toasts}=roomClient({network:true});
  c.connect('1');await sockets[0].open();element('box').value='🚀'.repeat(16001);c.send();
  assert.equal(sockets[0].frames.length,0);assert.ok(toasts.at(-1).includes('16000'));
  assert.equal([...element('box').value].length,16001);
  element('box').value='🚀'.repeat(16000);c.send();assert.equal(sockets[0].frames.length,1);
  assert.equal([...sockets[0].frames[0].content].length,16000);assert.equal(element('box').value.length,32000);
  assert.ok(!html.includes('maxlength="16000"'));
});

test('a socket send exception keeps the draft and retries its existing identifier',async()=>{
  const fixture=roomClient({network:true}),{context:c,element,read,sockets}=fixture;
  c.connect('1');await sockets[0].open();element('box').value='Keep this text';
  sockets[0].send=()=>{throw Error('fixture disconnect');};c.send();
  const requestID=read('pendingMessages.get("1").requestId');
  assert.equal(element('box').value,'Keep this text');assert.equal(sockets[0].readyState,3);
  await fixture.runTimers();await sockets[1].open();
  assert.equal(sockets[1].frames[0].requestId,requestID);
  await sockets[1].deliver({type:'room_receipt',groupId:1,requestId:requestID,status:'accepted',message:question});
  assert.equal(element('box').value,'');
});

test('cross-app Room draft requires the selected group and parent, and never sends',()=>{
 const fixture=roomClient(),{context:c,element,read,dispatch,sockets}=fixture;
 c.connect('2');
 const event={origin:c.location.origin,source:c.parent,data:{type:'room-draft',group:2,text:'Review call #8'}};
 dispatch('message',{...event,origin:'https://untrusted.test'});assert.equal(element('box').value,'');
 dispatch('message',{...event,data:{...event.data,group:3}});assert.equal(element('box').value,'');
 dispatch('message',event);assert.equal(element('box').value,'@agent Review call #8');
 assert.equal(sockets[0].frames.length,0);assert.equal(read('pendingMessages.size'),0);
 dispatch('message',{...event,data:{...event.data,text:'Replace existing draft'}});assert.equal(element('box').value,'@agent Review call #8');
});
