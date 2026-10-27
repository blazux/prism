const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const html = fs.readFileSync(path.join(__dirname, '../apps/room.html'), 'utf8');

function roomClient() {
  const nodes = new Map();
  const element = id => {
    if (!nodes.has(id)) nodes.set(id, {
      hidden:true, disabled:false, innerHTML:'', textContent:'', style:{}, value:'',
      scrollHeight:0, scrollTop:0, clientHeight:0,
      classList:{contains:()=>false,add(){},remove(){}},
      addEventListener(){},querySelectorAll:()=>[],querySelector:()=>null,focus(){},
    });
    return nodes.get(id);
  };
  const context = {
    document:{getElementById:element,querySelector:()=>null,addEventListener(){}},
    navigator:{language:'fr'}, Date, Map, Set, JSON, console,
    setTimeout,clearTimeout,
    PrismAPI:{toast(){},api(){throw Error('Unexpected request');}},
  };
  vm.createContext(context);
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)];
  const script = scripts.at(-1)[1].replace(/init\(\)\s*$/, '');
  vm.runInContext(script, context);
  // This test drives the real frame handler and render functions. The browser
  // fixture checks DOM updates; here only the full-list DOM rebuild is stubbed.
  vm.runInContext('renderAll = () => {}; renderTyping = () => {}; ME = {uid:1,name:"Vincent"}', context);
  return {context,element,read:expression=>vm.runInContext(expression,context)};
}
const question = {id:1,groupId:1,authorId:1,authorName:'Vincent',content:'@assistant salut',isAgent:false,createdAt:'2026-10-26T23:45:00-04:00'};
const pending = {id:2,groupId:1,authorName:'Assistant',content:'',isAgent:true,status:'running',replyTo:1,createdAt:'2026-10-26T23:45:01-04:00'};

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
