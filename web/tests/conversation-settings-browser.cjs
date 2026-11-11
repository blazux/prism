const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'..');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try {
  const context=await browser.newContext({viewport:{width:1280,height:850}}),page=await context.newPage(),errors=[],writes=[];
  let multi=false,failMemory=false,failContext=false,failMCP=false,revision='original',fact='Lives in Martinique';
  const reads=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{
   window.marked={parse:text=>text.replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;')};
   window.fixtureSockets=[];
   window.WebSocket=class {static OPEN=1;static CLOSED=3;constructor(){this.readyState=1;this.messages=[];fixtureSockets.push(this);queueMicrotask(()=>this.onopen?.())}send(raw){this.messages.push(JSON.parse(raw))}close(){this.readyState=3;this.onclose?.()}};
   window.deliverFixture=msg=>fixtureSockets.at(-1).onmessage({data:JSON.stringify(msg)});
  });
  await page.route('**/*',async route=>{
   const req=route.request(),url=new URL(req.url());
   if(url.hostname!=='fixture.invalid')return route.abort();
   if(url.pathname==='/app.js')return route.fulfill({contentType:'text/javascript',body:fs.readFileSync(path.join(root,'app.js'),'utf8')+'\nwindow.fixtureChat={reconnect:connect,reset:clearChat,goto:gotoSession,get open(){return chatOpen},get files(){return pendingFiles},set files(v){pendingFiles=v}};'});
   if(!url.pathname.startsWith('/api/')) {
    const file=path.join(root,url.pathname==='/'?'index.html':url.pathname);
    return fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''});
   }
   let data={},status=200;
   const mutation=!['GET','HEAD'].includes(req.method());
   if(!mutation)reads.push(url.pathname+url.search);
   if(mutation)writes.push({path:url.pathname,query:url.search,body:req.postDataJSON()});
   if(url.pathname==='/api/me')data={authenticated:true,multiUser:multi,isAdmin:!multi,user:multi?{id:1,role:'member',displayName:'Vincent'}:undefined};
   else if(url.pathname==='/api/auth')data={authenticated:true,auth_enabled:false};
   else if(url.pathname==='/api/platform')data={disabledApps:[],voxDocked:false};
   else if(url.pathname==='/api/profile')data={userId:multi?1:0,displayName:'Vincent',groups:[]};
   else if(url.pathname==='/api/my/groups')data={groups:[]};
   else if(url.pathname==='/api/sessions')data={sessions:[{id:'default',name:'Main',...(url.searchParams.has('includeSystem')?{runStatus:'running'}:{})},{id:'secondary',name:'Secondary'}]};
   else if(url.pathname==='/api/sessions/default/activity')data={runStatus:'running',request:'Prepare the weekly report',recentTools:[{tool:'read_file',status:'running'}],messages:[],subagents:[]};
   else if(url.pathname==='/api/activity')data={events:[]};
   else if(url.pathname==='/api/models')data={models:['fixture']};
   else if(url.pathname==='/api/agent/name')data={name:'Fixture'};
   else if(url.pathname==='/api/agent/limits')data={default:75,min:10,max:500,thinking:true,promptProfile:'standard'};
   else if(url.pathname==='/api/agent/personality')data={personality:'Base personality'};
   else if(url.pathname==='/api/vox-workspace')data={available:false};
   else if(url.pathname==='/api/chat/context') {
    if(mutation&&failContext){status=409;data={error:'Wait until this conversation finishes'};}
    else data={session:url.searchParams.get('session'),summary:'Summary of '+url.searchParams.get('session'),archiveSummary:'Earlier exchanges'};
   } else if(url.pathname==='/api/rag/upload/progress')data={found:true,stage:'done',total:1};
   else if(url.pathname==='/api/resources')data=[];
   else if(url.pathname==='/api/mcp/servers/image-gen')data={count:1,tools:[{name:'generate_image'}]};
   else if(url.pathname==='/api/mcp/servers'&&failMCP){status=502;data={error:'MCP registry unavailable'};}
   else if(url.pathname==='/api/mcp/servers')data={servers:[{id:'image-gen',name:'Image generator',sessionId:'global',enabled:true,url:'https://fixture.invalid/mcp',tools:[{name:'generate_image'}]}]};
   else if(url.pathname==='/api/memory') {
    const kind=url.searchParams.get('kind');
    if(mutation){if(failMemory){status=502;data={error:'Embedding provider unavailable'};}else{fact=req.postDataJSON().content;revision='updated';data={ok:true}}}
    else data=kind?{content:fact,revision}:{scope:multi?'u1':'',profile:[{filename:'Location <img src=x>',id:1}],learnings:[],knowledgeAvailable:true};
   } else if(url.pathname==='/api/skills')data=url.searchParams.has('name')?{skill:{name:'procedure',body:'Original steps',description:'Example'}}:{skills:[{name:'procedure',description:'Example'}]};
   return route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)});
  });
  await page.goto('http://fixture.invalid/');
  await page.waitForFunction(()=>fixtureSockets.length>0);
  await page.evaluate(()=>{if(!fixtureChat.open)toggleChat();document.getElementById('chat-input').value='First request';sendChat()});
  let first=await page.evaluate(()=>fixtureSockets.at(-1).messages.find(m=>m.type==='chat'));
  assert(first.requestId);assert.equal(await page.locator('#chat-input').inputValue(),'First request');
  assert.equal(await page.locator('#chat-messages .user').count(),0);
  await page.evaluate(id=>deliverFixture({type:'chat_receipt',requestId:id,status:'accepted',replayed:false}),first.requestId);
  assert.equal(await page.locator('#chat-input').inputValue(),'');assert.equal(await page.locator('#chat-messages .user').count(),1);
  await page.evaluate(()=>{document.getElementById('chat-input').value='Uncertain request';sendChat();fixtureSockets.at(-1).close();fixtureChat.reconnect()});
  await page.waitForFunction(()=>fixtureSockets.at(-1).messages.some(m=>m.type==='chat_request_status'));
  const pending=await page.evaluate(()=>fixtureSockets.at(-1).messages.find(m=>m.type==='chat_request_status').requestId);
  assert.equal(await page.evaluate(()=>fixtureSockets.flatMap(s=>s.messages).filter(m=>m.type==='chat').length),2,'reconnection must not resend automatically');
  await page.evaluate(id=>deliverFixture({type:'chat_receipt',requestId:id,status:'unknown'}),pending);
  await page.locator('#chat-delivery-status button').click();
  assert.equal(await page.evaluate(()=>fixtureSockets.at(-1).messages.find(m=>m.type==='chat').requestId),pending,'manual retry reuses identifier');
  await page.evaluate(id=>{deliverFixture({type:'chat_receipt',requestId:id,status:'completed',replayed:true});deliverFixture({type:'chat_history',messages:[{role:'user',content:'Uncertain request'},{role:'assistant',content:'Latest answer'}],hasMore:true,before:100})},pending);
  assert.equal(await page.locator('#chat-messages .user').count(),1,'a recovered receipt must not add a second bubble');
  await page.evaluate(()=>{fixtureChat.reset();deliverFixture({type:'chat_history',messages:[{role:'assistant',content:'Latest answer'}],hasMore:true,before:100})});
  await page.locator('#chat-history-more').click();
  assert.equal(await page.evaluate(()=>fixtureSockets.at(-1).messages.at(-1).before),100);
  await page.evaluate(()=>deliverFixture({type:'chat_history',prepend:true,messages:[{role:'user',content:'Uncertain request'},{role:'tool',toolName:'add_attachment',content:JSON.stringify({attachment:{name:'report.txt',url:'/api/chat/attachment?id=old'}})}],hasMore:false,before:50}));
  assert.equal(await page.locator('#chat-messages .assistant .chat-attachment-card').count(),1,'attachment follows its answer across pages');
  assert.equal(await page.locator('#chat-history-more').isVisible(),false);
  await page.evaluate(()=>{document.getElementById('chat-input').value='Unsent draft';fixtureChat.files=[{name:'kept.txt',path:'uploads/kept.txt'}];fixtureChat.goto('secondary');fixtureChat.goto('default')});
  assert.equal(await page.locator('#chat-input').inputValue(),'Unsent draft');assert.equal(await page.evaluate(()=>fixtureChat.files[0].name),'kept.txt');
  await page.locator('#chat-summary-button').click();
  const summary=page.locator('#chat-summary');await summary.locator('textarea').first().waitFor();
  assert.equal(await summary.locator('textarea').first().inputValue(),'Summary of default');
  await summary.locator('textarea').first().fill('Corrected conversation summary');
  failContext=true;await summary.getByRole('button',{name:'Save correction'}).first().click();
  await summary.getByRole('status').filter({hasText:'Wait until this conversation finishes'}).waitFor();
  assert.equal(await summary.locator('textarea').first().inputValue(),'Corrected conversation summary');
  failContext=false;await summary.getByRole('button',{name:'Save correction'}).first().click();
  await summary.getByRole('status').filter({hasText:/^Saved$/}).waitFor();
  assert(writes.some(w=>w.path==='/api/chat/context'&&w.body.content==='Corrected conversation summary'));
  await page.evaluate(()=>fixtureChat.goto('secondary'));assert.equal(await summary.isVisible(),false);
  await page.locator('#chat-summary-button').click();await summary.locator('textarea').first().waitFor();
  assert.equal(await summary.locator('textarea').first().inputValue(),'Summary of secondary');
  await page.screenshot({path:'/tmp/prism-chat-summary-desktop.png',fullPage:true});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  await page.screenshot({path:'/tmp/prism-chat-summary-mobile.png',fullPage:true});
  await page.setViewportSize({width:1280,height:850});
  await page.locator('#settings-link').click();
  const settingsURL=new URL(await page.locator('#app-frame').getAttribute('src'),'http://fixture.invalid');
  assert.equal(settingsURL.search,'','opening Settings does not carry a workspace');
  assert.equal(new URL(await page.locator('#settings-link').getAttribute('href'),'http://fixture.invalid').search,'');
  const contextsBeforeSettings=reads.filter(p=>p.startsWith('/api/chat/context')).length;
  await page.goto('http://fixture.invalid/settings.html?session=obsolete#knowledge');
  assert.equal(await page.locator('#session-badge').count(),0);
  assert.equal(await page.getByText('Context ·').count(),0);
  assert.equal(reads.filter(p=>p.startsWith('/api/chat/context')).length,contextsBeforeSettings);
  assert(reads.includes('/api/memory'));
  await page.evaluate(async()=>{
    const progress=document.createElement('span');window.fixtureProgress=progress;
    await pollIngestProgress('manual','readme.txt',progress,{done:false});
  });
  assert(reads.includes('/api/rag/upload/progress?collection=manual&filename=readme.txt'));

  const memory=page.locator('details.memory-card');await memory.locator('summary').click();
  await memory.locator('textarea').fill('Corrected fact');
  failMemory=true;await memory.getByRole('button',{name:'Save correction'}).click();
  await page.waitForFunction(()=>document.getElementById('settings-save-status').textContent.includes('Could not save'));
  assert.equal(await memory.locator('textarea').inputValue(),'Corrected fact');assert.equal(await page.evaluate(()=>PrismSettingsState.dirty()),true);
  failMemory=false;await memory.getByRole('button',{name:'Save correction'}).click();
  await page.waitForFunction(()=>document.getElementById('settings-save-status').textContent==='Saved');
  assert.equal(fact,'Corrected fact');assert.equal(await page.evaluate(()=>PrismSettingsState.dirty()),false);
  assert.equal(await memory.locator('summary img').count(),0,'memory names are text');
  await page.locator('[data-sub=skills]').click();await page.locator('.sk-view').click();await page.locator('.sk-body').fill('Corrected procedure');await page.locator('.sk-save').click();
  assert(writes.some(w=>w.path==='/api/skills'&&w.body.skill.body==='Corrected procedure'));
  await page.locator('#settings-nav .settings-advanced summary').click();assert(await page.locator('[data-tab=mcp]').isVisible());
  await page.locator('[data-tab=mcp]').click();await page.getByText('Image generator',{exact:true}).waitFor();
  assert(reads.includes('/api/mcp/servers'));assert(!reads.some(p=>p.startsWith('/api/mcp/servers?')));
  await page.getByRole('button',{name:'Refresh',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Refreshed · 1 tool'}).waitFor();
  assert(writes.some(w=>w.path==='/api/mcp/servers/image-gen'&&w.query===''));
  await page.locator('[data-tab=resources]').click();await page.getByText('No resources match this filter.').waitFor();
  assert(reads.includes('/api/resources'));assert(!reads.some(p=>p.startsWith('/api/resources?session=')));
  failMCP=true;await page.goto('http://fixture.invalid/settings.html?session=another#mcp');
  await page.getByRole('alert').filter({hasText:'Could not load MCP servers'}).waitFor();
  assert.equal(await page.getByText('No MCP server configured.',{exact:true}).count(),0);
  failMCP=false;await page.goto('http://fixture.invalid/settings.html#mcp');await page.getByText('Image generator',{exact:true}).waitFor();

  await page.screenshot({path:'/tmp/prism-simplification-settings-desktop.png',fullPage:true});
  await page.setViewportSize({width:390,height:844});assert((await page.locator('#settings-nav').boundingBox()).height<65,'mobile navigation stays on a single row');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  await page.screenshot({path:'/tmp/prism-simplification-settings-mobile.png',fullPage:true});
  multi=true;await page.goto('http://fixture.invalid/settings.html?mode=shared#knowledge');await page.locator('details.memory-card').waitFor();assert.equal(await page.locator('[data-tab=ai]').isVisible(),false);
  await page.goto('http://fixture.invalid/apps/activity.html');await page.getByRole('button',{name:'In progress'}).click();await page.getByText('Prepare the weekly report').waitFor();assert(await page.getByText('read_file · running').isVisible());
  assert.deepEqual(errors,[]);
  console.log('Conversation/settings: receipts, reconnect without resend, retry IDs, paginated attachments, workspace drafts, memory failure/retry, skill edits, scopes, mobile layout and active work passed.');
  await context.close();
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
