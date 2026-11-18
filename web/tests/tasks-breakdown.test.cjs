const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../apps/tasks.html'),'utf8');
const start=source.indexOf('async function addBreakdown() {'),code=source.slice(start,source.indexOf('\n}',start)+2);
function fixture(){
 const choices=['First','Second','Third'].map((_,i)=>({checked:true,disabled:false,dataset:{i:String(i)}}));
 const fields={'bd-list':{querySelectorAll:()=>choices.filter(c=>c.checked&&!c.disabled)},'bd-go':{disabled:false},'bd-err':{textContent:''}};
 const requests=[],closed=[],loads=[];const ctx=vm.createContext({$:id=>fields[id],fetch:(url,options)=>new Promise((resolve,reject)=>requests.push({title:JSON.parse(options.body).title,resolve,reject})),closeBreakdown:()=>closed.push(true),loadTodos:()=>loads.push(true)});
 vm.runInContext("let bdSuggestions=['First','Second','Third'];"+code,ctx);
 return {choices,fields,requests,closed,loads,run:s=>vm.runInContext(s,ctx)};
}
const tick=()=>new Promise(setImmediate);
test('partial HTTP and network failures keep rejected tasks and retry only unconfirmed entries',async()=>{
 const f=fixture(),first=f.run('addBreakdown()');await f.run('addBreakdown()');assert.equal(f.requests.length,1);
 f.requests[0].resolve(new Response('{}',{status:200}));await tick();f.requests[1].resolve(new Response('Read-only',{status:403}));await tick();f.requests[2].reject(Error('offline'));await first;
 assert.equal(f.fields['bd-go'].disabled,false);assert.equal(f.closed.length,0);assert.equal(f.choices[0].disabled,true);assert.equal(f.choices[0].checked,false);assert.equal(f.choices[1].checked,true);assert.equal(f.choices[2].checked,true);assert.match(f.fields['bd-err'].textContent,/Read-only/);assert.match(f.fields['bd-err'].textContent,/offline/);
 const retry=f.run('addBreakdown()');assert.equal(f.requests[3].title,'Second');f.requests[3].resolve(new Response('{}'));await tick();assert.equal(f.requests[4].title,'Third');f.requests[4].resolve(new Response('{}'));await retry;
 assert.deepEqual(f.requests.map(r=>r.title),['First','Second','Third','Second','Third']);assert.equal(f.closed.length,1);assert.equal(f.fields['bd-go'].disabled,false);assert.equal(f.loads.length,2);
});
