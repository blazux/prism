const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('fs'),path=require('path');
const root=path.resolve(__dirname,'../..');
const read=p=>fs.readFileSync(path.join(root,p),'utf8');
const constant=(s,n)=>s.split('const '+n+' = `')[1].split('`')[0];
const settingsSource=read('internal/server/telephony_app.go'),formSource=read('internal/server/admin_console_telephony.go');
const adminSource=read('internal/server/admin_console_page.go');
const adminPage=constant(adminSource,'adminConsoleHead')+constant(adminSource,'adminConsoleMid')+constant(adminSource,'adminConsoleTail');
const settings=constant(settingsSource,'telephonySettingsHead')+constant(formSource,'adminTelephonyPane').replace('class="pane"','class="pane active"')+constant(settingsSource,'telephonySettingsScript')+constant(formSource,'adminTelephonyJS')+'\nloadTelephony();</script></body></html>';
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try{
  for(const role of ['admin','member','personal','disconnected']){
   const context=await browser.newContext({locale:'fr-FR',viewport:{width:1100,height:850}}),page=await context.newPage(),errors=[],requests=[];
   const admin=['admin','personal'].includes(role);
   page.on('pageerror',e=>errors.push(e.message));
   await page.route('**/*',route=>{
    const r=route.request(),u=new URL(r.url());assert.equal(u.hostname,'fixture.invalid');requests.push(u.pathname);
    if(u.pathname==='/admin')return route.fulfill({contentType:'text/html',body:adminPage});
    if(u.pathname==='/')return route.fulfill({contentType:'text/html',body:read('web/apps/telephony.html')});
    if(u.pathname==='/apps/telephony.html')return route.fulfill({contentType:'text/html',body:read('web/apps/telephony.html')});
    if(u.pathname==='/apps/telephony/settings')return route.fulfill({status:admin?200:403,contentType:'text/html',body:admin?settings:'Forbidden'});
    if(['/widget-base.css','/style.css','/admin-console.css','/theme.js','/modal.js','/apps/telephony.css'].includes(u.pathname))return route.fulfill({path:path.join(root,'web',u.pathname)});
    let data={};
    if(u.pathname==='/api/me')data={authenticated:true,multiUser:true,user:{role:'global_admin',displayName:'Fixture'}};
    else if(u.pathname==='/api/platform')data={voxDocked:true};
    else if(u.pathname==='/api/vox-workspace')data={available:role!=='disconnected',admin,multiUser:role!=='personal',groups:[{id:2,name:'Betatest'}]};
    else if(u.pathname==='/api/vox-workspace/conversation')data={items:[{id:1,role:'user',content:'Combien d’appels sortants cette semaine ?'},{id:2,role:'assistant',content:'Le standard a passé 18 appels sortants cette semaine.',tools:[{name:'history',arguments:{direction:'outbound'},result:'{"total":18}'}]}],has_more:false};
    else if(u.pathname==='/api/vox-workspace/history')data={total:1,items:[{id:1,caller_number:'0596123456',direction:'inbound',started_at:'2026-10-28T12:00:00Z',duration_secs:24}],next_offset:null};
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
   if(role==='disconnected'){
    await page.locator('#error').filter({hasText:'pas connecté'}).waitFor();assert.equal(await page.locator('#send').isEnabled(),false);
   }else{
    await page.locator('#appTitle').filter({hasText:'Téléphonie'}).waitFor();
    assert.equal(requests.filter(p=>p==='/theme.js').length,1,'theme loaded more than once');
    assert.equal(await page.locator('#settings').isVisible(),admin);
    await page.getByRole('button',{name:'Appels',exact:true}).click();await page.locator('#calls tr.call').waitFor();
    assert.equal(await page.locator('#chat').isVisible(),false);
    if(admin){
     await page.getByRole('button',{name:'Paramètres',exact:true}).click();
     const editor=page.frameLocator('#settingsFrame');await editor.locator('#tel-persona').filter({}).waitFor();
     await editor.locator('#tel-model').waitFor({state:'visible'});
     assert.equal(await editor.locator('#adm-nav').count(),0);
     assert.equal(await page.locator('#groups').isVisible(),false);
     for(const theme of ['prism-dark','prism-light','graphite']){
      await page.evaluate(id=>PrismTheme.applyTheme(id),theme);
      const vars=await page.evaluate(()=>PrismTheme.activeVars());
      const normalize=value=>page.evaluate(v=>{const el=document.createElement('span');el.style.color=v;document.body.append(el);const color=getComputedStyle(el).color;el.remove();return color},value);
      assert.equal(await page.locator('body').evaluate(el=>getComputedStyle(el).backgroundColor),await normalize(vars['--bg1']));
      assert.equal(await editor.locator('body').evaluate(el=>getComputedStyle(el).color),await normalize(vars['--text']));
      assert.equal(await page.locator('#settings').evaluate(el=>getComputedStyle(el).color),await normalize(vars['--accent']));
      if(role==='admin')await page.screenshot({path:'/tmp/prism-telephony-'+theme+'.png',fullPage:true,animations:'disabled'});
     }
     // Theme previews are broadcast without writing localStorage.
     await page.evaluate(()=>window.postMessage({type:'prism-theme',vars:{'--accent':'#b23689'}},location.origin));
     await editor.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
     assert.equal(await editor.locator('body').evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--accent').trim()),'#b23689');
     await page.evaluate(()=>PrismTheme.applyFont('Georgia'));
     await editor.locator('body').evaluate(()=>new Promise(resolve=>requestAnimationFrame(resolve)));
     assert.ok((await page.locator('body').evaluate(el=>getComputedStyle(el).fontFamily)).includes('Georgia'));
     assert.ok((await editor.locator('body').evaluate(el=>getComputedStyle(el).fontFamily)).includes('Georgia'));
     await page.evaluate(()=>PrismTheme.applyFont(''));
     await page.evaluate(()=>PrismTheme.applyTheme('prism-dark'));
     for(const width of [1100,390,320]){
      await page.setViewportSize({width,height:850});
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'app overflow at '+width);
     }
     if(role==='admin'){await page.screenshot({path:'/tmp/prism-telephony-app-mobile.png',fullPage:true});await page.setViewportSize({width:1100,height:850});await page.screenshot({path:'/tmp/prism-telephony-app-desktop.png',fullPage:true});}
    }else assert.ok(!requests.includes('/apps/telephony/settings'));
    await page.getByRole('button',{name:'Supervision',exact:true}).click();assert.equal(await page.locator('#compose').isVisible(),true);
    if(role==='admin'){
     for(const theme of ['prism-dark','prism-light','graphite']){
      await page.evaluate(id=>PrismTheme.applyTheme(id),theme);
      await page.screenshot({path:'/tmp/prism-telephony-chat-'+theme+'.png',fullPage:true,animations:'disabled'});
     }
    }
    for(const width of [1100,390,320]){
     await page.setViewportSize({width,height:850});
     assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'chat overflow at '+width);
    }
    await page.locator('#message').focus();await page.keyboard.press('Tab');
    assert.equal(await page.locator('#send').evaluate(el=>getComputedStyle(el).outlineStyle),'solid');
   }
   if(role==='admin'){await page.goto('http://fixture.invalid/admin');await page.locator('#adm-nav .nav-item').first().waitFor();assert.equal(await page.locator('#adm-nav [data-pane="telephony"]').count(),0);await page.goto('http://fixture.invalid/admin#telephony');await page.waitForURL('**/?app=telephony&tab=settings');}
   assert.deepEqual(errors,[],role);await context.close();
  }
  console.log('Telephony app: admin/member/personal/disconnected, tabs, isolated settings and mobile layout passed');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exit(1)});
