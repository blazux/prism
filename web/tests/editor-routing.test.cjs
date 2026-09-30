const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../app.js'),'utf8');
const start=source.indexOf('// Only the currently displayed built-in app can answer this browser\'s editor RPC.');
const end=source.indexOf('// ─── Server messages',start);

function fixture(){
 const posted=[],sent=[],timers=[];
 const child={postMessage:m=>posted.push(m)},socket={};
 const frame={contentWindow:child};
 const ctx={currentView:{type:'app',name:'notes'},ws:socket,location:{origin:'http://localhost'},Date,
  document:{getElementById:id=>id==='app-frame'?frame:null},
  send:m=>sent.push(m),setTimeout:(fn,delay)=>{const t={fn,delay};timers.push(t);return t},clearTimeout:()=>{}};
 vm.createContext(ctx);vm.runInContext(source.slice(start,end),ctx);
 return {ctx,child,socket,posted,sent,timers};
}

test('editor request waits for the active iframe readiness handshake and is delivered once',()=>{
 const f=fixture(),message={type:'editor_request',id:'editor-1',args:{action:'update',revision:'v1',body:'new'},expires_at:Date.now()+30000};
 f.ctx.requestActiveEditor(message);
 assert.equal(f.posted.length,0,'request raced the iframe listener');
 f.ctx.markEditorReady(f.child,'notes');
 assert.equal(f.posted.length,1);
 assert.equal(f.posted[0].type,'editor-request');
 assert.equal(f.posted[0].args.body,'new');
 f.ctx.markEditorReady(f.child,'notes');
 assert.equal(f.posted.length,1,'a repeated ready signal duplicated an update');
});

test('readiness from a stale or different app cannot receive an editor request',()=>{
 const f=fixture(),other={postMessage(){}};
 f.ctx.requestActiveEditor({type:'editor_request',id:'editor-2',args:{action:'read'},expires_at:Date.now()+30000});
 f.ctx.markEditorReady(other,'notes');
 f.ctx.markEditorReady(f.child,'email');
 assert.equal(f.posted.length,0);
 f.ctx.markEditorReady(f.child,'notes');
 assert.equal(f.posted.length,1);
});

test('a bridge that never becomes ready returns an explicit error before the server timeout',()=>{
 const f=fixture();
 f.ctx.requestActiveEditor({type:'editor_request',id:'editor-3',args:{action:'read'},expires_at:Date.now()+30000});
 assert.ok(f.timers[0].delay<=29750 && f.timers[0].delay>29000);
 f.timers[0].fn();
 assert.deepEqual(JSON.parse(JSON.stringify(f.sent)),[{type:'editor_response',id:'editor-3',data:{error:'The editor app did not become ready. Keep it open and read it again.'}}]);
});
