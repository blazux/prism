const {test}=require('node:test');
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../app.js'),'utf8');
const code=source.slice(source.indexOf('function editWorkspace(sess)'),source.indexOf('// Shared publications UI'));
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(fetcher) {
 const fields={'#ws-name':{value:'Renamed',focus(){}},'#ws-personality':{value:''},'#ws-cancel':{},'#ws-save':{},'#ws-status':{},'#ws-icons':{querySelectorAll:()=>[],appendChild(){}}};
 let overlay,created=0,icons=0;const title={textContent:'Original'},calls=[];
 const element=()=>({style:{},dataset:{},appendChild(){},addEventListener(){},remove(){this.removed=true},querySelector:s=>fields[s],querySelectorAll:()=>Object.values(fields).filter(el=>el!==fields['#ws-icons']),focus(){}});
 const ctx={document:{createElement:()=>{const el=element();if(++created===1)overlay=el;return el},body:{appendChild(){}},getElementById:()=>title},WS_ICONS:{},getWorkspaceIcon:()=>'',setWorkspaceIcon:()=>icons++,escAttr:s=>s,fetch:async(url,options)=>{calls.push({url,options});return fetcher(url,options)},setTimeout(){},loadSessions(){},currentView:{type:'board'},currentSessionID:'test',setChatAgentLabel(){}};
 vm.createContext(ctx);vm.runInContext(code,ctx);ctx.editWorkspace({id:'test',name:'Original'});
 return {fields,calls,title,get closed(){return !!overlay.removed},get icons(){return icons}};
}
test('HTTP failure leaves the workspace dialog open with its draft and original title',async()=>{
 const f=fixture(async(_url,options)=>options?new Response('{"error":"Save refused"}',{status:500}):new Response('{"personality":"Stored instructions"}'));
 await tick();f.fields['#ws-personality'].value='My edits';await f.fields['#ws-save'].onclick();
 assert.equal(f.closed,false);assert.equal(f.fields['#ws-personality'].value,'My edits');assert.equal(f.title.textContent,'Original');assert.match(f.fields['#ws-status'].textContent,/Save refused/);assert.equal(f.icons,0);assert.equal(f.fields['#ws-save'].disabled,false);
});
test('slow initial read cannot overwrite editable text or start a premature save',async()=>{
 let resolve;const f=fixture(()=>new Promise(r=>resolve=r));
 assert.equal(f.fields['#ws-personality'].disabled,true);assert.equal(f.fields['#ws-save'].disabled,true);
 await f.fields['#ws-save'].onclick();assert.equal(f.calls.length,1);
 resolve(new Response('{"personality":"Loaded instructions"}'));await tick();
 assert.equal(f.fields['#ws-personality'].value,'Loaded instructions');assert.equal(f.fields['#ws-personality'].disabled,false);
});
test('double save sends one mutation and success closes the dialog',async()=>{
 let resolve;const f=fixture(async(_url,options)=>options?new Promise(r=>resolve=r):new Response('{"personality":""}'));
 await tick();const first=f.fields['#ws-save'].onclick();const second=f.fields['#ws-save'].onclick();await second;
 assert.equal(f.calls.filter(c=>c.options).length,1);assert.equal(f.fields['#ws-name'].disabled,true);
 resolve(new Response('{}'));await first;assert.equal(f.closed,true);assert.equal(f.title.textContent,'Renamed');assert.equal(f.icons,1);
});
test('a failed initial read never overwrites unseen instructions on rename',async()=>{
 const f=fixture(async(_url,options)=>options?new Response('{}'):new Response('unavailable',{status:503}));
 await tick();assert.equal(f.fields['#ws-personality'].disabled,true);await f.fields['#ws-save'].onclick();
 assert.equal(f.closed,true);assert.equal(f.calls.filter(c=>c.options?.method==='POST').length,0);
});
