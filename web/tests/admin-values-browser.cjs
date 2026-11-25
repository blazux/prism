const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../..'),read=p=>fs.readFileSync(path.join(root,p),'utf8');
const constant=(s,n)=>s.split('const '+n+' = `')[1].split('`')[0];
const admin=read('internal/server/admin_console_page.go'),telephony=read('internal/server/admin_console_telephony.go'),teams=read('internal/server/admin_console_teams.go');
const html=constant(admin,'adminConsoleHead')+constant(telephony,'adminTelephonyPane')+constant(admin,'adminConsoleMid')+constant(teams,'adminTeamsJS')+constant(telephony,'adminTelephonyJS')+constant(admin,'adminConsoleTail');
const name="Clé d’Océane et d'Émile & \"été\" <test>",model="modèle d'Émile & \"test\"",tool="outil d'Émile & \"test\"",server="serveur d'Émile & \"test\"",filename="Guide d'Émile & \"été\".pdf";
const wait=async predicate=>{const until=Date.now()+7000;while(!predicate()){if(Date.now()>until)throw Error('Expected admin action was not received');await new Promise(resolve=>setTimeout(resolve,10));}};
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try{
  const page=await browser.newPage(),errors=[],writes=[];
  page.setDefaultTimeout(8000);
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/*',async route=>{
   const request=route.request(),url=new URL(request.url());
   assert.equal(url.hostname,'fixture.invalid');
   if(url.pathname==='/admin')return route.fulfill({contentType:'text/html',body:html});
   if(!url.pathname.startsWith('/api/')){
    const file=path.join(root,'web',url.pathname);
    return fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''});
   }
   if(request.method()!=='GET')writes.push({method:request.method(),path:url.pathname,query:Object.fromEntries(url.searchParams),body:request.postData()?JSON.parse(request.postData()):null});
   let data={};
   if(url.pathname==='/api/me')data={authenticated:true,multiUser:true,user:{id:1,role:'global_admin',displayName:'Fixture'}};
   else if(url.pathname==='/api/my/groups')data={groups:[]};
   else if(url.pathname==='/api/admin/groups')data={groups:[{id:3,name:'Fixture group',models:[model]}]};
   else if(url.pathname==='/api/admin/users')data={users:[]};
   else if(url.pathname==='/api/models')data={models:[model]};
   else if(url.pathname==='/api/group/secrets')data={secrets:[name]};
   else if(url.pathname==='/api/group/mcp')data={servers:[{id:server,name:server,url:'https://fixture.invalid/mcp',tools:[{name:tool}]}]};
   else if(url.pathname==='/api/admin/tool-policy'||url.pathname==='/api/group/tool-policy')data={tools:[{tool,access:'open'}]};
   else if(url.pathname==='/api/rag/collections')data=[{name,description:'Fixture description',doc_count:1}];
   else if(url.pathname==='/api/rag/documents')data=[{id:9,filename,size_bytes:42,chunk_count:1,updated_at:'2026-11-24T12:00:00Z'}];
   else if(url.pathname==='/api/admin/platform')data={apps:['email'],disabledApps:[],allModels:[model],allowedModels:[]};
   return route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(data)});
  });
  const open=async pane=>{await page.goto('http://fixture.invalid/admin?pane='+pane+'#'+pane);await page.locator('.nav-item[data-pane="'+pane+'"]').click();await page.locator('.pane[data-pane="'+pane+'"].active').waitFor();};
  const action=async callback=>{const before=writes.length;await callback();await wait(()=>writes.length>before);return writes.at(-1);};

  await open('secrets');const secretButton=page.locator('#gsecrets button');await secretButton.waitFor();
  assert.equal(await page.locator('#gsecrets b').textContent(),name);
  assert.equal(await secretButton.getAttribute('onclick'),null);
  let result=await action(()=>secretButton.click());
  assert.equal(result.method,'DELETE');assert.equal(result.query.name,name);

  await open('groups');const removeModel=page.locator('[data-remove-model]');await removeModel.waitFor();
  assert.equal(await removeModel.getAttribute('onclick'),null);
  result=await action(()=>removeModel.click());
  assert.deepEqual(result.body,{groupId:3,model,action:'remove'});

  await open('tools');const globalTool=page.locator('#gtools select');await globalTool.waitFor();
  assert.equal(await globalTool.getAttribute('onchange'),null);
  result=await action(()=>globalTool.selectOption('admin_only'));
  assert.deepEqual(result.body,{tool,access:'admin_only'});

  await open('mcp');const mcpTool=page.locator('#gmcp select[data-tool]');await mcpTool.waitFor();
  assert.equal(await mcpTool.getAttribute('onchange'),null);
  result=await action(()=>mcpTool.selectOption('disabled'));
  assert.deepEqual(result.body,{tool,access:'disabled'});
  await page.locator('#gmcp [data-action="remove"]').waitFor();
  result=await action(()=>page.locator('#gmcp [data-action="remove"]').click());
  assert.equal(result.method,'DELETE');assert.equal(result.query.id,server);

  await open('rag');await page.locator('#grag .docrow button').waitFor();
  await page.evaluate(()=>{PrismModal.confirm=async text=>{window.fixtureConfirmation=text;return true;};PrismModal.prompt=async()=>({description:'Updated fixture'});});
  assert.equal(await page.locator('#grag .docrow button').getAttribute('onclick'),null);
  result=await action(()=>page.locator('#grag .docrow button').click());
  assert.equal(result.method,'DELETE');assert.equal(result.query.id,'9');
  assert.equal(await page.evaluate(()=>fixtureConfirmation),'Delete \"'+filename+'\" from collection \"'+name+'\"?');
  await page.locator('#grag [data-action="description"]').waitFor();
  result=await action(()=>page.locator('#grag [data-action="description"]').click());
  assert.equal(result.body.name,name);assert.equal(result.body.description,'Updated fixture');

  await open('access');const groupTool=page.locator('#ac-tools input');await groupTool.waitFor({state:'attached'});
  assert.equal(await groupTool.getAttribute('onchange'),null);
  result=await action(()=>page.locator('#ac-tools .tgl').click());
  assert.deepEqual(result.body,{tool,restrict:true});

  await open('platform');const modelToggle=page.locator('#pmodels input');await modelToggle.waitFor({state:'attached'});
  assert.equal(await modelToggle.getAttribute('onchange'),null);
  result=await action(()=>page.locator('#pmodels .tgl').click());
  assert.deepEqual(result.body,{allowedModels:[model]});
  assert.deepEqual(errors,[]);
  console.log('Admin values: Unicode, apostrophes, quotes and markup stay text; secret/model/tool/MCP/document controls retain their exact values without inline JavaScript.');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
