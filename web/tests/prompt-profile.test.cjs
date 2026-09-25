const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');
const html=fs.readFileSync(path.join(__dirname,'../settings.html'),'utf8');
const start=html.indexOf('async function renderAgentTab(');
const end=html.indexOf('\nasync function ',start+1);
const source=html.slice(start,end);

test('profile UI migrates legacy choices, posts Minimal and rejects failed save',async()=>{
 for(const [limits,selected] of [[{},'guided'],[{leanPrompt:true},'standard'],[{promptProfile:'minimal'},'minimal']]) {
  const fields=new Map(),nodes=[],posts=[];
  const field=id=>{if(!fields.has(id))fields.set(id,{value:'',checked:false,addEventListener(type,fn){this[type]=fn}});return fields.get(id)};
  const context={document:{createElement(){const node={style:{},appendChild(){},querySelector:field};nodes.push(node);return node}},fetch:async(url,options)=>{
   if(options?.method==='POST'){posts.push(JSON.parse(options.body));return {ok:false,clone:()=>({text:async()=>JSON.stringify({error:'fixture save failed'})}),json:async()=>({error:'fixture save failed'})}}
   return {ok:true,json:async()=>url==='/api/agent/limits'?limits:{}};
  },showToast:()=>{},icon:()=>'',escHtml:x=>x,avatarMarkup:()=>'',setTimeout:()=>{}};
  vm.createContext(context);vm.runInContext(html.slice(html.indexOf('async function settingsFetch('),html.indexOf('function getConfig()'))+source,context);
  await context.renderAgentTab({appendChild(){}});
  assert.match(nodes[1].innerHTML,new RegExp('value="'+selected+'" selected'));
  field('#ag-profile').value='minimal';field('#ag-effort').value='';field('#ag-maxiter').value='';
  await field('#ag-limits-save').click();
  assert.equal(posts[0].promptProfile,'minimal');assert.equal(posts[0].leanPrompt,undefined);
  assert.match(field('#ag-limits-status').textContent,/Failed: fixture save failed/);
 }
});
