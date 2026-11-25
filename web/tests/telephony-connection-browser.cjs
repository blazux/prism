const {createPreferencesBackend} = require('./preferences-fixture.cjs')
// Connection and editing checks use only synthetic APIs; no model, SIP or TTS service is called.
const {chromium}=require('playwright'),assert=require('node:assert/strict'),fs=require('fs'),path=require('path');
const root=path.resolve(__dirname,'../..'),read=file=>fs.readFileSync(path.join(root,file),'utf8');
const constant=(source,name)=>source.split('const '+name+' = `')[1].split('`')[0];
const settingsSource=read('internal/server/telephony_app.go'),formSource=read('internal/server/admin_console_telephony.go');
const html=constant(settingsSource,'telephonySettingsHead')+constant(formSource,'adminTelephonyPane').replace('class="pane"','class="pane active"')+constant(settingsSource,'telephonySettingsScript')+constant(formSource,'adminTelephonyJS')+'\nloadTelephony();</script></body></html>';
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try{
  const page=await browser.newPage({viewport:{width:1100,height:900}}),errors=[],requests=[],writes=[];
  let supportsPairing=true,remoteDisconnectFailure=false;
  let connection={configured:false,reachable:false,authenticated:false,reverse_connected:false,url:'',user:'',prism_url:'',prism_public_url:'',model:''},servers=[];
  page.on('pageerror',error=>errors.push(error.message));
  const preferencesFixture = createPreferencesBackend({})
  await page.route('**/*',route=>{
    const preferenceResponse = preferencesFixture.handle(route)
    if (preferenceResponse) return preferenceResponse
   const request=route.request(),url=new URL(request.url());assert.equal(url.hostname,'fixture.invalid');requests.push(url.pathname);
   if(url.pathname==='/settings/telephony')return route.fulfill({contentType:'text/html',body:html});
   if(url.pathname==='/api/vox/voice/editor')return route.fulfill({contentType:'text/html',body:'<p>Fixture speech editor</p>'});
   if(!url.pathname.startsWith('/api/')){const file=path.join(root,'web',url.pathname);if(fs.existsSync(file)&&fs.statSync(file).isFile())return route.fulfill({path:file});return route.fulfill({status:404,body:''});}
   const body=request.postData()?request.postDataJSON():null;
   if(request.method()!=='GET')writes.push({path:url.pathname,method:request.method(),body});
   let data={};
   if(url.pathname==='/api/auth')data={authenticated:true};
   else if(url.pathname==='/api/me')data={authenticated:true,isAdmin:true,multiUser:false};
   else if(url.pathname==='/api/platform')data={voxDocked:connection.configured};
   else if(url.pathname==='/api/models')data={models:['source/model'],labels:{'source/model':'Fixture provider · model'}};
   else if(url.pathname==='/api/vox-connection/test')data={...connection,reachable:true,authenticated:true,pairing_supported:supportsPairing,...(!supportsPairing?{detail:'Update PrismVox to manage the two-way connection.'}:{})};
   else if(url.pathname==='/api/vox-connection'){
    if(request.method()==='PUT')connection={...connection,...body,configured:true,reachable:true,authenticated:true,reverse_connected:true,password_configured:true,password:undefined};
    if(request.method()==='DELETE')connection={...connection,configured:false,reachable:false,authenticated:false,reverse_connected:false};
    data={...connection,pairing_supported:supportsPairing,...(!supportsPairing?{detail:'Update PrismVox to manage the two-way connection.'}:{}),...(request.method()==='DELETE'&&remoteDisconnectFailure?{detail:'Disconnected in Prism. PrismVox could not be reached to update its connection.'}:{})};
   }
   else if(url.pathname==='/api/voice')data={personality:'Fixture receptionist',model:''};
   else if(url.pathname==='/api/voice/kb')data={choices:[]};
   else if(url.pathname==='/api/voice/directory')data={entries:[]};
   else if(url.pathname==='/api/vox/config')data={greeting:'Hello fixture',greeting_known:'Hello %s',rag_enabled:'false',mcp_enabled:'false'};
   else if(url.pathname==='/api/vox/contacts')data=[];
   else if(url.pathname==='/api/vox/sip')data={sip_transport_mode:'udp',sip_udp_port:'5090',sip_registrar_match:'192.0.2.0/24'};
   else if(url.pathname==='/api/vox/sip/status')data={endpoint_state:'online'};
   else if(url.pathname==='/api/vox/tts/voices')data={voices:['Fixture voice'],current:'Fixture voice'};
   else if(url.pathname==='/api/vox/mcp'){
    if(request.method()==='POST'){const record={...body,id:51,auth_token:'',has_auth_token:!!body.auth_token};servers.push(record);data={id:51};}else data=servers;
   }
   else if(url.pathname==='/api/vox/mcp/51'){
    if(request.method()==='PUT'){Object.assign(servers[0],body,{auth_token:'',has_auth_token:body.clear_auth_token?false:!!body.auth_token||servers[0].has_auth_token});data={ok:true};}
    if(request.method()==='DELETE'){servers=[];data={ok:true};}
   }
   else if(url.pathname.endsWith('/tools'))data=[{name:'fixture_lookup',description:'Read public information',enabled:true}];
   return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
  });
  await page.goto('http://fixture.invalid/settings.html#telephony');
  const editor=page.frameLocator('#telephony-settings-frame');
  await editor.locator('#vox-save').waitFor({state:'visible'});
  assert.equal(await page.locator('.nav-item[data-tab="telephony"]').isVisible(),true);
  assert.equal(await editor.locator('[data-tel-tab=agent]').isDisabled(),true);
  assert.ok(!requests.includes('/api/vox/config'));
  assert.equal(await editor.locator('#vox-prism-url').inputValue(),'','the browser origin must not become a Docker callback address');
  assert.equal(await editor.locator('#vox-public-url').inputValue(),'http://fixture.invalid');
  assert.equal(await editor.locator('#vox-prism-url').getAttribute('required'),'');
  await editor.locator('#vox-url').fill('http://vox.fixture');await editor.locator('#vox-user').fill('fixture');await editor.locator('#vox-password').fill(' fixture-only ');
  await editor.locator('#vox-test').click();await editor.locator('#vox-connection-status').filter({hasText:'not been saved'}).waitFor();
  assert.equal(writes.filter(write=>write.method==='PUT').length,0);
  await editor.locator('#vox-prism-url').fill('http://prism.fixture:8080');
  await editor.locator('#vox-save').click();await editor.locator('#vox-connection-status').filter({hasText:'Connection saved.'}).waitFor();
  assert.equal(writes.find(write=>write.path==='/api/vox-connection').body.password,' fixture-only ');
  assert.equal(await editor.locator('#vox-password').inputValue(),'');
  assert.equal(await editor.locator('#sip-udp_port').inputValue(),'5090');assert.equal(await editor.locator('#sip-registrar_match').inputValue(),'192.0.2.0/24');
  await editor.locator('[data-tel-tab=advanced]').click();assert.equal(await editor.locator('#cf-rag_enabled').isChecked(),false);assert.equal(await editor.locator('#cf-mcp_enabled').isChecked(),false);assert.equal(await editor.locator('#cf-llm_model_standard').isDisabled(),true);
  await editor.locator('[data-tel-tab=connection]').click();await editor.locator('#vox-url').fill('http://unsaved.fixture');
  await editor.locator('[data-tel-tab=agent]').click();await editor.locator('#vox-model').selectOption('source/model');await editor.getByRole('button',{name:'Save model',exact:true}).first().click();
  await editor.locator('#vox-model-status').filter({hasText:'Saved'}).waitFor();
  const modelWrite=writes.filter(write=>write.path==='/api/vox-connection').at(-1);assert.deepEqual(modelWrite.body,{model:'source/model'});assert.equal(connection.url,'http://vox.fixture');
  await editor.locator('#vox-model').selectOption('default');
  await editor.locator('[data-tel-tab=connection]').click();await editor.locator('#vox-url').fill('http://vox.fixture');await editor.locator('#vox-save').click();await editor.locator('#vox-connection-status').filter({hasText:'Connection saved.'}).waitFor();
  await editor.locator('[data-tel-tab=agent]').click();assert.equal(await editor.locator('#vox-model').inputValue(),'default','saving the connection discarded an independent model draft');await editor.locator('#vox-model').selectOption('source/model');await editor.locator('#tel-persona').fill('Edited receptionist');
  await page.locator('.nav-item[data-tab=profile]').click();await page.locator('.pm-overlay').waitFor();await page.getByRole('button',{name:'Cancel',exact:true}).click();
  await editor.getByRole('button',{name:'Save switchboard',exact:true}).click();await editor.locator('#tel-vmsg').filter({hasText:'Saved'}).waitFor();
  // The embedded speech editor participates in the same unsaved-change guard.
  await editor.locator('summary').filter({hasText:'Speech providers'}).click();const speech=editor.frameLocator('#tel-speech-providers');await speech.locator('p').waitFor();
  await speech.locator('body').evaluate(()=>parent.postMessage({type:'prismvox-voice-dirty',dirty:true},location.origin));
  await page.locator('.nav-item[data-tab=profile]').click();await page.locator('.pm-overlay').waitFor();await page.getByRole('button',{name:'Cancel',exact:true}).click();
  await speech.locator('body').evaluate(()=>parent.postMessage({type:'prismvox-voice-dirty',dirty:false},location.origin));
  await editor.locator('[data-tel-tab=access]').click();
  await editor.locator('summary').filter({hasText:'Add an external tool server'}).click();const create=editor.locator('#tel-mcp-new form');await create.getByLabel('Name',{exact:true}).fill('Fixture <b>server</b>');await create.getByLabel('URL',{exact:true}).fill('https://tools.fixture/mcp');await create.getByLabel('Bearer token',{exact:true}).fill('fixture-token');
  await create.getByRole('button',{name:'Add server',exact:true}).click();const card=editor.locator('#tel-mcp-list > details');await card.locator('summary').filter({hasText:'Fixture <b>server</b>'}).waitFor();await card.locator('summary').click();
  assert.equal(await card.locator('b').count(),0);assert.equal(await card.getByLabel('Bearer token',{exact:true}).inputValue(),'');
  await card.getByLabel('Name',{exact:true}).fill('Updated fixture');await card.getByRole('button',{name:'Save server',exact:true}).click();await card.locator('summary').filter({hasText:'Updated fixture'}).waitFor();
  await card.getByRole('button',{name:'Remove server',exact:true}).click();await editor.getByRole('button',{name:'Delete',exact:true}).click();await card.waitFor({state:'detached'});
  await page.locator('.nav-item[data-tab=profile]').click();await page.locator('#telephony-settings-frame').waitFor({state:'detached'});assert.equal(await page.locator('.pm-overlay').count(),0);
  // A lost remote disconnect is not presented as a complete two-way disconnect.
  remoteDisconnectFailure=true;await page.locator('.nav-item[data-tab=telephony]').click();await editor.locator('#vox-disconnect').waitFor({state:'visible'});
  await editor.locator('#vox-disconnect').click();await editor.getByRole('button',{name:'OK',exact:true}).click();
  await editor.locator('#vox-connection-status').filter({hasText:'PrismVox could not be reached'}).waitFor();
  await editor.locator('#vox-save').click();await editor.locator('#vox-connection-status').filter({hasText:'Connection saved.'}).waitFor();
  assert.equal(connection.model,'source/model','reconnecting replaced the saved phone model');
  assert.equal(writes.filter(write=>write.path==='/api/vox-connection'&&write.method==='PUT').at(-1).body.model,undefined);
  // An older Vox remains editable, but cannot claim to support pairing or the model relay.
  supportsPairing=false;connection.model='';await page.reload();await editor.locator('#vox-connection-detail').filter({hasText:'Update PrismVox'}).waitFor();
  assert.equal(await editor.locator('#vox-save').isDisabled(),true);assert.equal(await editor.locator('#vox-test').isEnabled(),true);assert.equal(await editor.locator('#vox-disconnect').isEnabled(),true);
  await editor.locator('[data-tel-tab=agent]').click();assert.equal(await editor.locator('#vox-model').isDisabled(),true);assert.equal(await editor.locator('#vox-model-save').isDisabled(),true);
  await editor.locator('[data-tel-tab=advanced]').click();assert.equal(await editor.locator('#cf-llm_model_standard').isEnabled(),true);
  await editor.locator('[data-tel-tab=connection]').click();await editor.locator('#vox-url').fill('http://other-legacy.fixture');assert.equal(await editor.locator('#vox-save').isEnabled(),true);
  await editor.locator('#vox-test').click();await editor.locator('#vox-connection-status').filter({hasText:'not been saved'}).waitFor();assert.equal(await editor.locator('#vox-save').isDisabled(),true);
  assert.deepEqual(errors,[]);console.log('Telephony connection, guarded drafts, MCP CRUD, partial disconnect, reconnection and legacy compatibility passed');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});
