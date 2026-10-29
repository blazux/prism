const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('fs'),path=require('path');
const root=path.resolve(__dirname,'../..'),read=p=>fs.readFileSync(path.join(root,p),'utf8');
const constant=(s,n)=>s.split('const '+n+' = `')[1].split('`')[0];
const settingsSource=read('internal/server/telephony_app.go'),formSource=read('internal/server/admin_console_telephony.go'),adminSource=read('internal/server/admin_console_page.go');
const pane=constant(formSource,'adminTelephonyPane'),script=constant(formSource,'adminTelephonyJS');
const adminPage=constant(adminSource,'adminConsoleHead')+pane+constant(adminSource,'adminConsoleMid')+script+constant(adminSource,'adminConsoleTail');
const settings=constant(settingsSource,'telephonySettingsHead')+pane.replace('class="pane"','class="pane active"')+constant(settingsSource,'telephonySettingsScript')+script+'\nloadTelephony();</script></body></html>';
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try{
  for(const role of ['admin','member','personal','disconnected']){
   const context=await browser.newContext({locale:'fr-FR',viewport:{width:1100,height:850}}),page=await context.newPage(),errors=[],requests=[],writes=[];
   const personal=role==='personal',admin=['admin','personal'].includes(role),connected=role!=='disconnected';
   let queue=[{id:42,phone_number:'0596123456',mission:'Fixture mission <b>safe</b>',scheduled_at:'2030-11-01T13:00:00Z',status:'pending',attempts:0,max_attempts:3}];
   page.on('pageerror',e=>{errors.push(e.message);console.error(role+': '+e.message)});
   await page.route('**/*',route=>{
    const r=route.request(),u=new URL(r.url());assert.equal(u.hostname,'fixture.invalid');requests.push(u.pathname);
    if(u.pathname==='/admin')return route.fulfill({contentType:'text/html',body:adminPage});
    if(u.pathname==='/settings/telephony')return route.fulfill({status:personal?200:403,contentType:'text/html',body:personal?settings:'Forbidden'});
    if(!u.pathname.startsWith('/api/')){
     const file=path.join(root,'web',u.pathname==='/'?'apps/telephony.html':u.pathname);
     if(fs.existsSync(file)&&fs.statSync(file).isFile())return route.fulfill({path:file});
     return route.fulfill({status:404,body:''});
    }
    let data={};
    if(u.pathname==='/api/auth')data={authenticated:true};
    else if(u.pathname==='/api/me')data={authenticated:true,isAdmin:admin,multiUser:!personal,...(personal?{}:{user:{role:role==='admin'?'global_admin':'user',displayName:'Fixture'}})};
    else if(u.pathname==='/api/platform')data={voxDocked:connected};
    else if(u.pathname==='/api/my/groups')data={groups:[]};
    else if(u.pathname==='/api/admin/groups')data={groups:[{id:2,name:'Betatest'}]};
    else if(u.pathname==='/api/vox-workspace')data={available:connected,admin,multiUser:!personal,groups:[{id:2,name:'Betatest'}]};
    else if(u.pathname==='/api/vox-workspace/access')data={availableGroups:[{id:2,name:'Betatest'}],groups:[2]};
    else if(u.pathname==='/api/vox-workspace/conversation')data={items:[{id:1,role:'user',content:'Combien d’appels sortants cette semaine ?'},{id:2,role:'assistant',content:'Le standard a passé 18 appels sortants cette semaine.',tools:[{name:'history',arguments:{direction:'outbound'},result:'{"total":18}'}]}],has_more:false};
    else if(u.pathname==='/api/vox-workspace/history')data={total:1,items:[{id:1,caller_number:'0596123456',direction:'inbound',started_at:'2026-10-28T12:00:00Z',ended_at:null,duration_secs:24}],next_offset:null};
    else if(u.pathname.startsWith('/api/vox-workspace/calls')){
     if(r.method()!=='GET'){
      const body=r.postData()?r.postDataJSON():null;writes.push({method:r.method(),path:u.pathname,body});
      if(r.method()==='DELETE')queue[0].status='cancelled';
      else if(u.pathname.endsWith('/retry'))queue[0].status='pending';
      else queue=[{...body,id:43,status:'pending',attempts:0}];
     }
     data={total:queue.length,items:queue};
    }
    else if(u.pathname==='/api/models')data={models:['fixture-model']};
    else if(u.pathname==='/api/voice')data={personality:'Fixture personality',model:'fixture-model'};
    else if(u.pathname==='/api/voice/kb')data={choices:[]};
    else if(u.pathname==='/api/voice/directory')data={entries:[]};
    else if(u.pathname==='/api/vox/contacts')data={items:[]};
    else if(u.pathname==='/api/vox/sip/status')data={endpoint_state:'online'};
    else if(u.pathname==='/api/vox/tts/voices')data={voices:['Fixture voice'],current:'Fixture voice'};
    return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
   });
   await page.goto('http://fixture.invalid/apps/telephony.html');
   if(!connected){
    await page.locator('#error').filter({hasText:'pas connecté'}).waitFor();assert.equal(await page.locator('#scheduleCall').isEnabled(),false);
   }else{
    await page.locator('#calls tr.call').waitFor();
    assert.equal(await page.locator('#settings,#settingsFrame,#accessButton').count(),0);
    assert.equal(await page.locator('#logs').isVisible(),true);
    assert.ok(await page.locator('#calls').textContent().then(s=>s.includes('En cours')));
    await page.getByRole('button',{name:'Appels sortants',exact:true}).click();await page.locator('.outbound-call').waitFor();
    assert.equal(await page.locator('#groups').isVisible(),false);
    assert.equal(await page.locator('.outbound-call b').count(),0);
    await page.getByRole('button',{name:'Annuler',exact:true}).click();await page.getByRole('button',{name:'Relancer maintenant'}).waitFor();
    await page.getByRole('button',{name:'Relancer maintenant'}).click();await page.getByRole('button',{name:'Annuler',exact:true}).waitFor();
    await page.locator('#callPhone').fill('0596654321');await page.locator('#callMission').fill('Confirmer le rendez-vous');await page.locator('#callAt').fill('2030-11-01T09:00');
    await page.getByRole('button',{name:'Programmer l’appel',exact:true}).click();await page.locator('#callFeedback').filter({hasText:'Appel programmé'}).waitFor();
    assert.ok(writes.some(w=>w.method==='POST'&&w.path.endsWith('/calls')&&w.body.mission==='Confirmer le rendez-vous'&&w.body.scheduled_at));
    for(const theme of ['prism-dark','prism-light','graphite']){
     await page.evaluate(id=>PrismTheme.applyTheme(id),theme);
     for(const width of [1100,390,320]){await page.setViewportSize({width,height:850});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'outbound overflow at '+width);}
     if(role==='admin')await page.screenshot({path:'/tmp/prism-telephony-operations-'+theme+'.png',fullPage:true,animations:'disabled'});
    }
    await page.getByRole('button',{name:'Supervision',exact:true}).click();assert.equal(await page.locator('#compose').isVisible(),true);
    await page.locator('#message').focus();await page.keyboard.press('Tab');assert.equal(await page.locator('#send').evaluate(el=>getComputedStyle(el).outlineStyle),'solid');
    assert.ok(!requests.includes('/api/voice'),'the operations app fetched configuration');
    await page.setViewportSize({width:1100,height:850});
    if(role==='admin'){
     await page.goto('http://fixture.invalid/admin#telephony');await page.locator('#tel-persona').waitFor({state:'visible'});
     assert.equal(await page.locator('#adm-nav [data-pane="telephony"]').count(),1);
     await page.locator('#tel-access summary').click();await page.locator('#tel-access-choices input').waitFor();
    }
    await page.goto('http://fixture.invalid/settings.html#telephony');
    if(personal){
     await page.locator('.nav-item[data-tab="telephony"]').waitFor({state:'visible'});
     const editor=page.frameLocator('#telephony-settings-frame');await editor.locator('#tel-model').waitFor({state:'visible'});
     assert.equal(await editor.locator('#adm-nav').count(),0);assert.equal(await editor.locator('#tel-access').isVisible(),false);
     for(const theme of ['prism-dark','prism-light','graphite']){
      await page.evaluate(id=>PrismTheme.applyTheme(id),theme);
      await editor.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(resolve)));
      const expected=await page.evaluate(()=>PrismTheme.activeVars()['--accent']);
      assert.equal(await editor.locator('body').evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--accent').trim()),expected);
     }
    }else{
     assert.equal(await page.locator('.nav-item[data-tab="telephony"]').isVisible(),false);
     assert.equal(await page.locator('#telephony-settings-frame').count(),0);
    }
   }
   assert.deepEqual(errors,[],role);await context.close();
  }
  console.log('Telephony: operations-only app, scoped scheduling/cancel/retry, Admin in multi-user, Settings in personal mode, themes and mobile layout passed');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exit(1)});
