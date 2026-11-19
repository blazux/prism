const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const widget=fs.readFileSync(path.join(__dirname,'../prism-widget.js'),'utf8');
const app=fs.readFileSync(path.join(__dirname,'../app.js'),'utf8');

function fixture(standalone=false){
 const listeners=new Set(),streams=[],requests=[];
 const win={PRISM_SESSION:'u7-board',addEventListener:(type,fn)=>{if(type==='message')listeners.add(fn)},removeEventListener:(type,fn)=>listeners.delete(fn)};
 win.parent=standalone?win:{};
 class EventSource{
  constructor(url){this.url=url;this.handlers={};streams.push(this)}
  addEventListener(type,fn){this.handlers[type]=fn}
  close(){this.closed=true}
  deliver(data){this.handlers.change({data:JSON.stringify(data)})}
 }
 const ctx=vm.createContext({window:win,document:{addEventListener(){}},EventSource,setTimeout,fetch:async(url,options)=>{requests.push({url,...options});return {ok:true,json:async()=>({accepted:true})}}});
 vm.runInContext(widget,ctx);
 return {win,streams,listeners,requests,ctx,deliver(data,source=win.parent){for(const fn of [...listeners])fn({data,source})}};
}

test('embedded widgets share the dashboard socket, filter topics/scopes and unsubscribe',()=>{
 const f=fixture(),all=[],notes=[],group=[];
 const stopAll=f.win.prismOnData(e=>all.push(e));
 const stopNotes=f.win.prismOnData(e=>notes.push(e),'notes');
 const stopGroup=f.win.prismOnData(e=>group.push(e),{topic:'todo.shared',scope:'group',group:3});
 assert.equal(f.streams.length,0);assert.equal(f.listeners.size,1);
 f.deliver({type:'data-changed',topic:'notes',scope:'personal'},{evil:true});
 assert.equal(all.length,0);
 f.deliver({type:'data-changed',topic:'notes',scope:'personal'});
 f.deliver({type:'data-changed',topic:'todo.shared',scope:'personal'});
 f.deliver({type:'data-changed',topic:'todo.shared',scope:'group',group:4});
 f.deliver({type:'data-changed',topic:'todo.shared',scope:'group',group:3});
 assert.equal(all.length,4);assert.equal(notes.length,1);assert.equal(group.length,1);
 f.deliver({type:'data-changed',topic:'*',reason:'reconnected'});
 assert.equal(notes.at(-1).topic,'*');assert.equal(group.at(-1).reason,'reconnected');
 stopNotes();stopGroup();stopAll();stopAll();assert.equal(f.listeners.size,0);
 f.deliver({type:'data-changed',topic:'notes'});assert.equal(notes.length,2);
});

test('standalone previews use one reconnecting SSE stream and release it on last unsubscribe',()=>{
 const f=fixture(true),events=[];
 const a=f.win.prismOnData(e=>events.push(e),'notes'),b=f.win.prismOnData(()=>{});
 assert.equal(f.streams.length,1);assert.equal(f.streams[0].url,'/api/data-events?session=u7-board');
 f.streams[0].deliver({type:'data_changed',topic:'*',reason:'connected'});
 f.streams[0].deliver({type:'data_changed',topic:'notes',scope:'personal'});
 f.streams[0].handlers.change({data:'broken JSON'});assert.equal(events.length,2);
 a();assert.equal(f.streams[0].closed,undefined);b();assert.equal(f.streams[0].closed,true);
 const c=f.win.prismOnData(()=>{});assert.equal(f.streams.length,2);c();
});

test('custom data publication uses the session and rejects failed saves/signals explicitly',async()=>{
 const f=fixture();
 await f.win.prismDataChanged('todo.shared',{scope:'group',group:3});
 const req=f.requests[0];assert.equal(req.url,'/api/data-events?session=u7-board');
 assert.equal(req.method,'POST');assert.deepEqual(JSON.parse(req.body),{topic:'todo.shared',scope:'group',group:3});
 assert.equal(req.headers.Authorization,undefined);
 f.ctx.fetch=async()=>({ok:false,status:403,json:async()=>({error:'not a member of this group'})});
 await assert.rejects(f.win.prismDataChanged('todo.shared'),/not a member/);
 f.ctx.fetch=async()=>({ok:false,status:500,json:async()=>{throw Error('bad JSON')}});
 await assert.rejects(f.win.prismDataChanged('todo.shared'),/HTTP 500/);
});

function functionSource(name){
 const start=app.indexOf('function '+name+'(');assert.ok(start>=0,name);
 return app.slice(start,app.indexOf('\n}',start)+2);
}
test('dashboard invalidations fan out to widgets and matching app without an agent turn',()=>{
 const appMessages=[],widgets=[[],[]];
 const c={currentView:{type:'app',name:'notes'},location:{origin:'https://prism.test'},document:{getElementById:()=>({contentWindow:{postMessage:(...args)=>appMessages.push(args)}}),querySelectorAll:()=>widgets.map(messages=>({contentWindow:{postMessage:(...args)=>messages.push(args)}}))}};
 vm.createContext(c);vm.runInContext(functionSource('notifyDataChanged'),c);
 c.notifyDataChanged({type:'data_changed',topic:'tasks',scope:'personal'});
 assert.equal(appMessages.length,0);assert.equal(widgets[0].length,1);assert.equal(widgets[1].length,1);
 c.notifyDataChanged({type:'data_changed',topic:'notes',scope:'group',group:3});
 assert.equal(appMessages.length,1);assert.equal(appMessages[0][0].topic,'notes');assert.equal(appMessages[0][1],'https://prism.test');
 c.notifyDataChanged({topic:'*',reason:'reconnected'});assert.equal(appMessages.length,2);
 assert.equal(widgets[0].at(-1)[0].reason,'reconnected');
 assert.match(app,/case 'data_changed':\s*notifyDataChanged\(msg\)/);
 assert.match(app,/ws\.onopen\s*=.*notifyDataChanged\(\{topic:'\*',reason:'reconnected'\}\)/);
});

test('cron and email retain legacy completion refresh while committed native writes use server signals',()=>{
 const events=[],c={currentView:{type:'board'},notifyDataChanged:event=>events.push(event)};
 vm.createContext(c);
 const start=app.indexOf('const PIM_TOOL_APP ='),end=app.indexOf('function notifyDataChanged(',start);
 vm.runInContext(app.slice(start,end),c);
 for(const tool of ['note','task','calendar','cron','email']){
  c.trackPimTool({id:tool,tool,input:{action:'add'}});c.maybeRefreshApp({id:tool});
 }
 assert.deepEqual(events.map(e=>e.topic),['tasks','email']);
 c.trackPimTool({id:'read',tool:'email',input:{action:'list'}});c.maybeRefreshApp({id:'read'});
 assert.equal(events.length,2);
});
