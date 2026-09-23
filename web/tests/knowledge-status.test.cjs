const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const source = fs.readFileSync(require('node:path').join(__dirname, '../settings.html'), 'utf8');
const start = source.indexOf('async function renderRAGTab(container) {');
const end = source.indexOf("  const tab = document.createElement('div')", start);
const statusOnly = source.slice(start, end) + '\n}';
test('Knowledge polls a stable notice, stops when detached, and renders once ready', async () => {
 const timers = [], notices = [];
 let ready = false, requests = 0, writes = 0, replacements = 0;
 const container = {
  appendChild(node) { notices.push(node); },
  replaceChildren() { replacements++; notices.forEach(n => n.isConnected = false); }
 };
 const ctx = {currentTab:'knowledge', computeRagManage:async()=>{},
  fetch:async()=> {requests++; return {ok:true,json:async()=>({ready,status:'configuration failed'})};},
  setTimeout:f=>timers.push(f),
  document:{createElement:()=>({style:{},isConnected:true,set textContent(v){writes++;this.text=v;},get textContent(){return this.text;}})}
 };
 vm.createContext(ctx);vm.runInContext(statusOnly,ctx);
 await ctx.renderRAGTab(container);
 assert.equal(notices.length,1);
 await timers.shift()();
 assert.equal(notices.length,1);assert.equal(writes,1);assert.equal(replacements,0);
 ready=true;await timers.shift()();
 assert.equal(replacements,1);assert.equal(timers.length,0);
 ready=false;await ctx.renderRAGTab(container);
 notices.at(-1).isConnected=false;
 const before=requests;await timers.shift()();assert.equal(requests,before);assert.equal(timers.length,0);
});
