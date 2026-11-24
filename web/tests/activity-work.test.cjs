const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../apps/activity-work.js'),'utf8');
const links=fs.readFileSync(path.join(__dirname,'../object-links.js'),'utf8');
class Element{constructor(tag){this.tag=tag;this.children=[];this.dataset={};this.style={};this.textContent='';}append(...nodes){this.children.push(...nodes);}}
function fixture(){
 const status={style:{},textContent:''},feed={querySelector:()=>({}),untouched:true};
 const ctx=vm.createContext({URL,URLSearchParams,location:{origin:'https://prism.test',search:''},document:{createElement:tag=>new Element(tag),addEventListener(){}},addEventListener(){},feed,statusEl:status,moreBtn:{style:{}},PrismAPI:{api:async()=>{throw Error('offline');}}});
 ctx.window=ctx;ctx.parent=ctx;vm.runInContext(links,ctx);vm.runInContext(source,ctx);
 return {ctx,status,feed,run:s=>vm.runInContext(s,ctx),json:s=>JSON.parse(JSON.stringify(vm.runInContext(s,ctx)))};
}
const text=element=>element.textContent+' '+element.children.map(text).join(' ');
test('work groups distinguish user attention, active turns and finished turns',()=>{
 const f=fixture();f.run("var items=[{id:'a',name:'A',runStatus:'waiting_for_approval'},{id:'b',name:'B',runStatus:'running'},{id:'c',name:'C',runStatus:'completed'},{id:'d',name:'D',runStatus:'interrupted'},{id:'e',runStatus:'idle'}]");
 assert.deepEqual(f.json('groupAgentWork(items).attention.map(i=>i.id)'),['a','d']);
 assert.deepEqual(f.json('groupAgentWork(items).running.map(i=>i.id)'),['b']);
 assert.deepEqual(f.json('groupAgentWork(items).finished.map(i=>i.id)'),['c']);
});
test('conversation links accept local workspaces and Rooms and reject unsafe destinations',()=>{
 const f=fixture();
 assert.equal(f.run("workLink('/?workspace=finance&chat=open')"),'/?workspace=finance&chat=open');
 assert.equal(f.run("workLink('/?app=room&group=7')"),'/?app=room&group=7');
 for(const bad of ['javascript:alert(1)','https://other.test/?workspace=x','https://name:pass@prism.test/?workspace=x','/?app=room&group=0','/?app=email&id=12'])assert.equal(f.run(`workLink(${JSON.stringify(bad)})`),null);
});
test('legacy terminal states do not invent elapsed time or event dates',()=>{
 const f=fixture();f.ctx.Set=Set;
 const legacy=f.run("renderWorkCard({id:'legacy',name:'Legacy',runStatus:'completed'},'2026-11-24T14:20:00Z',new Set())");
 assert.doesNotMatch(text(legacy),/Elapsed|Duration|Started|Ended|Last event/);
 const failed=f.run("renderWorkCard({id:'failed',name:'Failed',runStatus:'failed',startedAt:'2026-11-24T12:00:00Z'},'2026-11-24T14:20:00Z',new Set())");
 assert.doesNotMatch(text(failed),/Elapsed|Duration/);
 const done=f.run("renderWorkCard({id:'done',runStatus:'completed',startedAt:'2026-11-24T14:18:00Z',finishedAt:'2026-11-24T14:19:00Z'},'2026-11-24T14:20:00Z',new Set())");
 assert.match(text(done),/Duration 1m 0s/);
});
test('failed refresh preserves the last work snapshot and marks it as stale',async()=>{
 const f=fixture();f.run("workSnapshot={items:[{id:'retained',runStatus:'running'}],observedAt:'2026-11-24T14:20:00Z'}");
 await f.run('loadAgentWork()');assert.equal(f.feed.untouched,true);
 assert.equal(f.run('workSnapshot.items[0].id'),'retained');
 assert.match(f.status.textContent,/Showing the last observation/);assert.match(f.status.textContent,/offline/);
});
