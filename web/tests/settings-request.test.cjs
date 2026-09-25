const {test}=require('node:test');
const assert=require('node:assert/strict'), vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const html=fs.readFileSync(path.join(__dirname,'../settings.html'),'utf8');
const helper=html.slice(html.indexOf('async function settingsFetch('),html.indexOf('function getConfig()'));
test('settings mutations reject HTTP and network failures instead of reporting success',async()=>{
 const notices=[];let response;
 const ctx={fetch:async()=>{if(response instanceof Error)throw response;return response;},showToast:m=>notices.push(m)};
 vm.createContext(ctx);vm.runInContext(helper,ctx);
 for(const method of ['POST','PUT','PATCH','DELETE']) {
  response=new Response(JSON.stringify({error:'Save refused'}),{status:403});
  await assert.rejects(ctx.settingsFetch('/api/config',{method}),/Save refused/);
 }
 response=new Error('Network unavailable');await assert.rejects(ctx.settingsFetch('/api/config',{method:'POST'}),/Network unavailable/);
 assert.equal(notices.length,5);
 response=new Response('{}',{status:200});assert.equal(await ctx.settingsFetch('/api/config',{method:'POST'}),response);
});
