const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const src=fs.readFileSync(path.join(__dirname,'../../internal/server/admin_console_page.go'),'utf8');
const ctx={esc:s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;'),JSON,String};
vm.createContext(ctx);
vm.runInContext(src.slice(src.indexOf('function telToolTraceHTML(t)'),src.indexOf('async function loadTelephonyUsage()')),ctx);
test('phone logs show tool actions, outcomes and results as text',()=>{
 const html=ctx.telToolTraceHTML({tool:'calendar',args:{action:'add',title:'<img src=x>'},outcome:'failed',result:'<script>bad()</script>'});
 assert.ok(html.includes('calendar · add — Failed'));
 assert.ok(html.includes('&lt;img'));
 assert.ok(html.includes('&lt;script&gt;'));
 assert.ok(!html.includes('<img')&&!html.includes('<script>'));
 assert.ok(ctx.telToolTraceHTML({tool:'note',outcome:'started'}).includes('Result not received'));
 assert.ok(ctx.telToolTraceHTML({tool:'note',result:'fixture',result_truncated:true}).includes('Result (truncated)'));
});
