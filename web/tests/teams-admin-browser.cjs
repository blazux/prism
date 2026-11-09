const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../..'),read=p=>fs.readFileSync(path.join(root,p),'utf8');
const constant=(s,n)=>s.split('const '+n+' = `')[1].split('`')[0];
const admin=read('internal/server/admin_console_page.go'),telephony=read('internal/server/admin_console_telephony.go'),teams=read('internal/server/admin_console_teams.go');
const html=constant(admin,'adminConsoleHead')+constant(telephony,'adminTelephonyPane')+constant(admin,'adminConsoleMid')+constant(teams,'adminTeamsJS')+constant(telephony,'adminTelephonyJS')+constant(admin,'adminConsoleTail');
const app='11111111-1111-1111-1111-111111111111',tenant='22222222-2222-2222-2222-222222222222';
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try{
  const context=await browser.newContext({viewport:{width:1200,height:850}}),page=await context.newPage(),errors=[],writes=[];
  let failSave=false,delayGroup3=false,releaseGroup3;
  const delayed=new Promise(resolve=>releaseGroup3=resolve);
  const configs={3:{appId:app,tenantId:tenant,ragRead:'*',ragWrite:'',tools:'',ownerConversation:'conversation-3'},5:{appId:'55555555-5555-5555-5555-555555555555',tenantId:tenant,ragRead:'',ragWrite:'',tools:'',ownerConversation:''}};
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',async route=>{
   const r=route.request(),u=new URL(r.url());assert.equal(u.hostname,'fixture.invalid');
   if(u.pathname==='/admin')return route.fulfill({contentType:'text/html',body:html});
   if(!u.pathname.startsWith('/api/')){const file=path.join(root,'web',u.pathname);return fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''});}
   let data={},status=200;const group=u.searchParams.get('group');
   if(u.pathname==='/api/me')data={authenticated:true,multiUser:true,user:{role:'global_admin',displayName:'Fixture'}};
   else if(u.pathname==='/api/admin/groups')data={groups:[{id:3,name:'Group A'},{id:5,name:'Group B'}]};
   else if(u.pathname==='/api/my/groups')data={groups:[]};
   else if(u.pathname==='/api/models')data={models:['fixture']};
   else if(u.pathname==='/api/room/config')data={agentName:'Prism'};
   else if(u.pathname==='/api/webex/config')data={configured:false};
   else if(u.pathname==='/api/teams/config'){
    if(r.method()==='POST'){const body=r.postDataJSON();writes.push({group,body});if(failSave){status=400;data={error:'Permissions must contain Entra user object IDs or *'};}else{if(!body.disconnect)configs[group]=body;data={ok:true};}}
    else{if(delayGroup3&&group==='3')await delayed;data={configured:true,hasSecret:true,config:configs[group],endpoint:'/api/teams/messages/'+group};}
   }else if(u.pathname==='/api/teams/conversations')data={conversations:[{id:'conversation-'+group,name:'Support <script>unsafe</script>'}]};
   return route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
  });
  await page.goto('http://fixture.invalid/admin#agent');await page.locator('#tm-save:not([disabled])').waitFor();
  assert.equal(await page.locator('#tm-secret').inputValue(),'');assert.equal(await page.locator('#tm-app').inputValue(),app);
  assert.match(await page.locator('#tm-state').textContent(),/Configuration saved/);assert.match(await page.locator('#tm-endpoint-help').textContent(),/not HTTPS/);
  assert.equal(await page.locator('#tm-room').inputValue(),'conversation-3');assert.equal(await page.locator('#tm-room script').count(),0);
  await page.locator('#tm-save').click();await page.locator('#tm-status').filter({hasText:'Saved'}).waitFor();
  assert.equal(writes.at(-1).group,'3');assert.equal(writes.at(-1).body.clientSecret,undefined);assert.equal(writes.at(-1).body.ownerConversation,'conversation-3');
  failSave=true;await page.locator('#tm-secret').fill('fixture-new-secret');await page.locator('#tm-tools').fill('bad-email@example.test');await page.locator('#tm-save').click();
  await page.locator('#tm-status').filter({hasText:'Entra user object IDs'}).waitFor();assert.equal(await page.locator('#tm-secret').inputValue(),'fixture-new-secret');assert.ok(!(await page.locator('#tm-status').textContent()).includes('✓'));
  failSave=false;
  // A slower response for group A must not overwrite the newly selected group B.
  delayGroup3=true;await page.evaluate(()=>{loadTeams();});await page.locator('#ag-group').selectOption('5');await page.locator('#tm-save:not([disabled])').waitFor();
  releaseGroup3();await page.waitForTimeout(100);assert.equal(await page.locator('#tm-app').inputValue(),configs[5].appId);assert.equal(await page.locator('#tm-secret').inputValue(),'');assert.match(await page.locator('#tm-endpoint').inputValue(),/messages\/5$/);
  await page.locator('#tm-save').click();await page.locator('#tm-status').filter({hasText:'Saved'}).waitFor();assert.equal(writes.at(-1).group,'5');assert.equal(writes.at(-1).body.appId,configs[5].appId);
  for(const theme of ['prism-dark','prism-light']){
   await page.evaluate(theme=>PrismTheme.applyTheme(theme),theme);await page.locator('#tm-app').scrollIntoViewIfNeeded();
   await page.screenshot({path:'/tmp/prism-teams-admin-'+theme+'.png',fullPage:true,animations:'disabled'});
  }
  assert.deepEqual(errors,[]);console.log('Teams admin: setup, secret retention, failure feedback, group-switch races, escaping and themes passed.');await context.close();
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
