const {test}=require('node:test');
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const helper=fs.readFileSync(path.join(__dirname,'../tool-preferences.js'),'utf8');
function fixture(config) {
 const data=new Map([['agent-config',JSON.stringify(config)]]);
 const context={window:{},localStorage:{getItem:key=>data.get(key),setItem:(key,value)=>data.set(key,value)}};
 vm.createContext(context);vm.runInContext(helper,context);
 return {prefs:context.window.PrismToolPreferences,data};
}
test('old disabled actions keep the corresponding combined capability off',()=>{
 const {prefs,data}=fixture({disabled:['remove_widget','cron_add','pip_install','rag_list','custom_api'],other:'preserved'});
 const cfg=JSON.parse(JSON.stringify(prefs.read()));
 assert.deepEqual(cfg.disabled,['widget','cron','install_packages','rag_manage','custom_api']);assert.equal(cfg.other,'preserved');
 assert.deepEqual(cfg.combinedToolNotices,['widget','cron','install_packages','rag_manage']);
 assert.deepEqual(JSON.parse(data.get('agent-config')),cfg);
 assert.deepEqual(JSON.parse(JSON.stringify(prefs.read())),cfg,'migration is idempotent');
 prefs.set('cron',true);assert.equal(prefs.read().disabled.includes('cron'),false);assert.equal(prefs.read().combinedToolNotices.includes('cron'),false);
 prefs.dismiss();assert.equal(prefs.read().combinedToolNotices,undefined);assert.equal(prefs.read().disabled.includes('widget'),true);
});
test('current and unknown tool preferences are preserved without migration notice',()=>{
 const {prefs}=fixture({disabled:['widget','custom_api','cron']});
 assert.deepEqual(JSON.parse(JSON.stringify(prefs.read())),{disabled:['widget','custom_api','cron']});
 prefs.set('widget',true);prefs.set('widget',false);assert.equal(prefs.read().disabled.filter(x=>x==='widget').length,1);
});
test('Settings switches refer to actual native tools; chat and settings share the same migration',()=>{
 const html=fs.readFileSync(path.join(__dirname,'../settings.html'),'utf8');
 const catalog=html.split('const TOOL_CATEGORIES = [')[1].split('async function renderResourcesTab')[0];
 const names=new Set([...fs.readFileSync(path.join(__dirname,'../../internal/agent/tools.go'),'utf8').matchAll(/Name:\s*"([^"]+)"/g)].map(m=>m[1]));
 for(const [,name]of catalog.matchAll(/id: '([^']+)'/g))assert(names.has(name),'unknown tool preference '+name);
 for(const file of ['index.html','settings.html'])assert.match(fs.readFileSync(path.join(__dirname,'../'+file),'utf8'),/src="\/tool-preferences.js"/);
 assert.match(fs.readFileSync(path.join(__dirname,'../app.js'),'utf8'),/function getDisabledTools\(\) \{ return PrismToolPreferences.read\(\).disabled/);
});

test('custom names inherited from Object.prototype remain disabled without migration',()=>{
 const {prefs,data}=fixture({disabled:['constructor','toString','hasOwnProperty','__proto__']});
 const disabled=['constructor','toString','hasOwnProperty','__proto__'];
 assert.deepEqual(JSON.parse(JSON.stringify(prefs.read())),{disabled});
 assert.deepEqual(JSON.parse(data.get('agent-config')),{disabled});
 prefs.set('toString',true);assert.equal(prefs.read().disabled.includes('toString'),false);
 assert.equal(prefs.read().disabled.includes('constructor'),true);
});
