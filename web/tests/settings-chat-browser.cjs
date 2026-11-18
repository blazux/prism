const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'..'),origin='http://fixture.invalid';

(async()=>{
 const executablePath=process.env.CHROMIUM_EXECUTABLE_PATH||process.env.CHROMIUM_EXECUTABLE;
 const browser=await chromium.launch({headless:true,...(executablePath?{executablePath}:{})});
 try {
  for(const multi of [false,true])for(const width of [1280,390]) {
   const label=(multi?'multi':'mono')+' '+width;
   const context=await browser.newContext({viewport:{width,height:850}});
   const page=await context.newPage(),errors=[],writes=[],reads=[];
   page.setDefaultTimeout(10000);
   page.on('pageerror',error=>errors.push(error.message));
   await context.addInitScript(()=>{
    localStorage.setItem('active-workspace','obsolete');
    window.fixtureDocument=Math.random();
    window.marked={parse:text=>text.replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;')};
    window.fixtureSockets=[];
    window.WebSocket=class {
     static OPEN=1;static CLOSED=3;
     constructor(url){this.url=url;this.readyState=1;this.messages=[];fixtureSockets.push(this);queueMicrotask(()=>this.onopen?.())}
     send(raw){this.messages.push(JSON.parse(raw))}
     close(){this.readyState=3;this.onclose?.()}
    };
   });
   await context.route('**/*',async route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.origin!==origin)return route.abort();
    if(url.pathname==='/app.js')return route.fulfill({contentType:'text/javascript',body:fs.readFileSync(path.join(root,'app.js'),'utf8')+
     '\nwindow.fixtureState={get session(){return currentSessionID},get view(){return currentView},get open(){return chatOpen}};'});
    if(!url.pathname.startsWith('/api/')) {
     const file=path.join(root,url.pathname==='/'?'index.html':url.pathname);
     return fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''});
    }
    if(!['GET','HEAD'].includes(request.method()))writes.push({path:url.pathname,method:request.method()});
    else reads.push(url.pathname+url.search);
    let data={};
    if(url.pathname==='/api/auth')data={authenticated:true,auth_enabled:true};
    else if(url.pathname==='/api/me')data={authenticated:true,multiUser:multi,isAdmin:!multi,user:multi?{id:1,role:'member',displayName:'Camille'}:undefined};
    else if(url.pathname==='/api/profile')data={userId:multi?1:0,displayName:'Camille',groups:[]};
    else if(url.pathname==='/api/platform')data={disabledApps:[],voxDocked:false};
    else if(url.pathname==='/api/my/groups')data={groups:[]};
    else if(url.pathname==='/api/sessions')data={sessions:[{id:multi?'u1-obsolete':'obsolete',name:'Unrelated workspace'}]};
    else if(url.pathname==='/api/agent/name')data={name:'Fixture'};
    else if(url.pathname==='/api/models')data={models:['fixture']};
    else if(url.pathname==='/api/vox-workspace')data={available:false};
    else if(url.pathname==='/api/email/config')data={configured:false,has_password:false};
    return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
   });
   try {
    await page.goto(origin+'/');
    await page.waitForFunction(()=>window.fixtureState?.view.type==='board'&&fixtureSockets.length>0);
    const documentId=await page.evaluate(()=>fixtureDocument);
    await page.locator('.rail-item[data-app="email"]').click();
    const email=page.frameLocator('#app-frame');
    const configure=email.getByRole('button',{name:'Open Settings → Email'});
    await configure.waitFor();
    await page.evaluate(()=>{
     const data={type:'open-settings',tab:'email'},source=document.getElementById('app-frame').contentWindow;
     dispatchEvent(new MessageEvent('message',{origin:location.origin,source:window,data}));
     dispatchEvent(new MessageEvent('message',{origin:'https://other.invalid',source,data}));
    });
    assert.equal(await page.evaluate(()=>fixtureState.view.type),'app',label+' ignores wrong source or origin');
    await configure.click();
    const settings=page.frameLocator('#app-frame');
    await settings.locator('#e-user').waitFor();
    assert.equal(await page.evaluate(()=>fixtureDocument),documentId,label+' Email → Settings must preserve the shell document');
    assert.equal(page.url(),origin+'/',label+' Email → Settings must not navigate the top page');
    let frameURL=new URL(await page.locator('#app-frame').getAttribute('src'),origin);
    assert.equal(frameURL.pathname,'/settings.html');assert.equal(frameURL.hash,'#email');
    assert.equal(frameURL.search,'',label+' settings must not carry the current workspace');
    assert.equal(await page.evaluate(()=>fixtureState.session),multi?'u1-assistant':'assistant',label+' settings use the general assistant');
    assert.equal(await settings.locator('#settings-chat-link').isVisible(),false,label+' embedded settings use the shell chat');
    assert(await page.locator('#chat-fab').isVisible(),label+' keeps the chat button');
    await page.locator('#chat-fab').click();
    await page.locator('#chat-drawer').waitFor({state:'visible'});
    await page.locator('#chat-input').fill('Help me configure my mailbox.');
    assert.equal(await page.locator('#chat-input').inputValue(),'Help me configure my mailbox.');

    await page.goto(origin+'/settings.html?session=obsolete#email');
    await page.locator('#e-user').waitFor();
    const chatLink=page.locator('#settings-chat-link');
    assert(await chatLink.isVisible(),label+' standalone settings expose the assistant link');
    await page.locator('#logout-btn').waitFor({state:'visible'});
    const headerBounds=await page.locator('#dash-header').evaluate(header=>{
     const selectors=['.header-left','#settings-chat-link','#logout-btn'];
     const headerRect=header.getBoundingClientRect();
     return selectors.map(selector=>{
      const bounds=header.querySelector(selector).getBoundingClientRect();
      return {selector,left:bounds.left,right:bounds.right,top:bounds.top,bottom:bounds.bottom,viewport:innerWidth,headerTop:headerRect.top,headerBottom:headerRect.bottom};
     });
    });
    if(width===390)await page.screenshot({path:'/tmp/prism-settings-chat-'+(multi?'multi':'mono')+'-390.png'});
    for(const bounds of headerBounds) {
     assert(bounds.left>=-1&&bounds.right<=bounds.viewport+1,label+' settings header control exceeds viewport: '+JSON.stringify(bounds));
     assert(bounds.top>=Math.max(0,bounds.headerTop)-1&&bounds.bottom<=bounds.headerBottom+1,label+' settings header clips its controls: '+JSON.stringify(bounds));
    }
    const linkURL=new URL(await chatLink.getAttribute('href'),origin);
    assert.equal(linkURL.searchParams.get('view'),'settings');
    assert.equal(linkURL.searchParams.get('tab'),'email');
    assert.equal(linkURL.searchParams.get('chat'),'open');
    assert.equal(linkURL.searchParams.has('session'),false);
    await page.locator('[data-tab="profile"]').click();
    assert.equal(new URL(await chatLink.getAttribute('href'),origin).searchParams.get('tab'),'profile',label+' link follows the visible settings section');
    await page.locator('[data-tab="email"]').click();
    await page.locator('#e-user').waitFor();
    await chatLink.click();
    await page.waitForFunction(()=>window.fixtureState?.view.type==='settings'&&fixtureState.open);
    await page.frameLocator('#app-frame').locator('#e-user').waitFor();
    assert.equal(context.pages().length,1,label+' opens the assistant in the same browser tab');
    assert.equal(await page.evaluate(()=>fixtureState.session),multi?'u1-assistant':'assistant');
    assert(await page.locator('#chat-drawer').isVisible());
    frameURL=new URL(await page.locator('#app-frame').getAttribute('src'),origin);
    assert.equal(frameURL.hash,'#email');assert.equal(frameURL.search,'');
    assert.equal(new URL(page.url()).searchParams.has('session'),false);
    assert.equal(await page.frameLocator('#app-frame').locator('#settings-chat-link').isVisible(),false);

    if(width===1280) {
     await page.goto(origin+'/apps/email.html?session=obsolete');
     await page.getByRole('button',{name:'Open Settings → Email'}).click();
     await page.waitForFunction(()=>window.fixtureState?.view.type==='settings');
     await page.frameLocator('#app-frame').locator('#e-user').waitFor();
     assert.equal(new URL(page.url()).searchParams.get('view'),'settings');
     assert.equal(new URL(page.url()).searchParams.get('tab'),'email');
     assert.equal(new URL(page.url()).searchParams.has('session'),false,label+' standalone Email fallback excludes the obsolete workspace');
    }
    assert(reads.includes('/api/email/config'));
    assert(!reads.some(url=>url.startsWith('/api/email/config?')),label+' mailbox reads are independent of workspace');
    assert.deepEqual(writes,[],label+' navigating and opening the assistant does not mutate configuration');
    assert.deepEqual(errors,[],label+' raises no JavaScript errors');
    console.log(label+': embedded and standalone Email/Settings navigation, assistant chat, scope and read-only checks passed.');
   } finally {await context.close()}
  }
 } finally {await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1});
