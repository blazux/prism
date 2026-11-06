const assert=require('node:assert/strict'),fs=require('fs');
const {chromium}=require('playwright');
const source=fs.readFileSync(require('path').join(__dirname,'../app.js'),'utf8');
function fn(name){const start=source.indexOf('function '+name+'(');const end=source.indexOf('\nfunction ',start+1);return source.slice(start,end<0?undefined:end)}
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try {
 const page=await browser.newPage({viewport:{width:420,height:500}});
 let savedImageBytes;
 await page.route('http://fixture.invalid/**',route=>{
  const url=new URL(route.request().url());
  if(url.pathname==='/api/chat/attachment'){
   if(url.searchParams.get('preview')==='1')return route.fulfill({contentType:'image/jpeg',body:Buffer.from(savedImageBytes,'base64')});
   return route.fulfill({headers:{'Content-Type':'application/pdf','Content-Disposition':'attachment; filename=report.pdf'},body:'fixture download'});
  }
  return route.fulfill({contentType:'text/html',body:'<div id="chat-messages" style="max-width:380px"></div>'});
 });
 await page.goto('http://fixture.invalid/');
 const install=async()=>{
 await page.addStyleTag({path:require('path').join(__dirname,'../style.css')});
 await page.addScriptTag({path:require('path').join(__dirname,'../theme.js')});
 await page.addScriptTag({content:`let pendingAttachments=[],currentAssistantEl=null,currentAssistantContent='';
 const chatWasAtBottom=()=>true,scrollChat=()=>{},setStreaming=()=>{},renderMarkdown=s=>s,fmtTime=()=>'',userChip=()=>'',agentChip=()=>'',AGENT_NAME='Agent',escHtml=s=>String(s),formatToolInput=()=>'';
 `+['showChatImagePreview','prepareChatImages','appendUserMessage','appendStream','appendResponseAttachment','appendToolUse','appendToolResult','finalizeStream','restoreChatHistory'].map(fn).join('\n')});
 };
 await install();
 await page.evaluate(()=>{pendingAttachments.push({path:'/api/chat/attachment?session=default&id=abc&name=a.txt',title:'a <b>.txt'});finalizeStream()});
 assert.equal(await page.locator('.assistant a .chat-attachment-name').textContent(),'a <b>.txt');
 assert.equal(await page.locator('.chat-attachment-meta').textContent(),'TXT · Download');assert.equal(await page.locator('b').count(),0);
 assert.equal(await page.locator('.assistant a').getAttribute('download'),'a <b>.txt');
 await page.evaluate(()=>{document.getElementById('chat-messages').innerHTML='';restoreChatHistory([{role:'tool',toolName:'add_attachment',content:JSON.stringify({attachment:{name:'report.pdf',url:'/api/chat/attachment?session=default&id=abc'}})}]);appendResponseAttachment(document.body,{path:'javascript:alert(1)',title:'unsafe'})});
 assert.equal(await page.locator('a').count(),1);assert.equal(await page.locator('a').getAttribute('download'),'report.pdf');
 const downloadEvent=page.waitForEvent('download');await page.locator('.chat-attachment-card').click();
 assert.equal((await downloadEvent).suggestedFilename(),'report.pdf');
 const imageBytes = await page.evaluate(()=>{
  const canvas=document.createElement('canvas');canvas.width=2400;canvas.height=1600;
  const ctx=canvas.getContext('2d');ctx.fillStyle='#9372c4';ctx.fillRect(0,0,canvas.width,canvas.height);
  return canvas.toDataURL('image/jpeg').split(',')[1];
 });
 savedImageBytes=imageBytes;
 await page.evaluate(bytes=>{document.getElementById('chat-messages').innerHTML='';pendingAttachments.push({images:[bytes],path:'/api/chat/attachment?id=abc',title:'image.png'});finalizeStream()},imageBytes);
 assert.equal(await page.locator('img').count(),1);assert.equal(await page.locator('a').count(),1);
 const thumbnail=page.locator('.chat-response-attachments img');
 await thumbnail.evaluate(image=>image.decode());
 const thumbWidth=(await thumbnail.boundingBox()).width;
 await thumbnail.click();
 assert(await page.locator('dialog.chat-image-preview').isVisible(),'attachment image opens on click');
 assert.equal(await page.locator('dialog img').getAttribute('src'),await thumbnail.getAttribute('src'));
 assert((await page.locator('dialog img').boundingBox()).width>thumbWidth,'preview enlarges the thumbnail');
 assert(await page.locator('dialog').evaluate(el=>el.scrollWidth<=el.clientWidth),'preview fits narrow screens');
 await page.evaluate(()=>{window.chatEscapeCount=0;document.addEventListener('keydown',e=>{if(e.key==='Escape')window.chatEscapeCount++})});
 await page.keyboard.press('Escape');
 await page.locator('dialog').waitFor({state:'detached'});
 assert.equal(await page.evaluate(()=>window.chatEscapeCount),0,'Escape must leave the chat dock open');
 assert(await thumbnail.evaluate(image=>document.activeElement===image),'focus returns to the image');
 await thumbnail.press('Enter');
 assert(await page.locator('dialog').isVisible(),'image opens with keyboard');
 await page.mouse.click(2,2);
 await page.locator('dialog').waitFor({state:'detached'});
 await thumbnail.press('Space');
 await page.getByRole('button',{name:'Close',exact:true}).click();
 await page.locator('dialog').waitFor({state:'detached'});
 await page.evaluate(bytes=>{
  restoreChatHistory([{role:'assistant',content:'<p><a href="/must-not-navigate"><img src="data:image/jpeg;base64,'+bytes+'" alt="Restored image"></a></p>'}]);
 },imageBytes);
 await page.getByRole('button',{name:'Enlarge Restored image',exact:true}).click();
 assert.equal(new URL(page.url()).pathname,'/','preview prevents an enclosing link from navigating');
 await page.keyboard.press('Escape');
 await page.locator('dialog').waitFor({state:'detached'});
 await page.evaluate(bytes=>{
  appendUserMessage('Photo',[bytes],[]);
  appendToolUse({id:'screenshot',tool:'browser',input:{}});
  appendToolResult({id:'screenshot',output:'Done',images:[bytes]});
 },imageBytes);
 for(const selector of ['.chat-msg-images img','#tool-screenshot img']){
  await page.locator(selector).click();
  assert(await page.locator('dialog').isVisible());
  await page.keyboard.press('Escape');
  await page.locator('dialog').waitFor({state:'detached'});
 }
 await page.evaluate(()=>{
  localStorage.setItem('history',JSON.stringify([
   {role:'user',content:'Draw a landscape'},
   {role:'tool',toolName:'add_attachment',content:JSON.stringify({attachment:{name:'landscape.png',url:'/api/chat/attachment?session=default&id=snapshot&name=landscape.png'}})},
   {role:'assistant',content:'Here is the landscape.'}
  ]));
 });
 for(let reload=0;reload<2;reload++){
  await page.reload();
  await install();
  await page.evaluate(()=>restoreChatHistory(JSON.parse(localStorage.getItem('history'))));
  const restored=page.locator('.assistant .chat-response-attachments img');
  await restored.evaluate(image=>{image.loading='eager';return image.decode()});
  assert.equal(await restored.count(),1,'one image restored under the assistant reply');
  assert((await restored.getAttribute('src')).endsWith('&preview=1'));
  assert.equal(await page.locator('.tool .chat-response-attachments').count(),0);
  await restored.click();
  await page.locator('dialog img').evaluate(image=>image.decode());
  assert(await page.locator('dialog img').evaluate(image=>image.complete&&image.naturalWidth>0));
  await page.keyboard.press('Escape');
  await page.locator('dialog').waitFor({state:'detached'});
  const restoredDownload=page.waitForEvent('download');
  await page.locator('.assistant .chat-attachment-card').click();
  await restoredDownload;
 }
 await page.evaluate(()=>{
  document.getElementById('chat-messages').innerHTML='';
  const reply=document.createElement('div');reply.className='chat-msg assistant';
  reply.textContent='Voici les fichiers demandés.';document.getElementById('chat-messages').append(reply);
  for(const name of ['Bilan des appels — octobre 2026.pdf','rendez-vous.csv'])appendResponseAttachment(reply,{path:'/api/chat/attachment?id=fixture',title:name});
 });
 const backgrounds=[];
 for(const theme of ['prism-dark','prism-light','graphite']){
  await page.evaluate(id=>PrismTheme.applyTheme(id),theme);
  backgrounds.push(await page.locator('.chat-attachment-card').first().evaluate(el=>getComputedStyle(el).backgroundColor));
  await page.screenshot({path:'/tmp/prism-attachment-'+theme+'.png',fullPage:true});
 }
 assert.equal(new Set(backgrounds).size,3,'cards must follow theme changes');
 await page.keyboard.press('Tab');
 assert.equal(await page.locator('.chat-attachment-card:focus-visible').evaluate(el=>getComputedStyle(el).outlineStyle),'solid');
 await page.setViewportSize({width:320,height:500});
 await page.evaluate(()=>appendResponseAttachment(document.getElementById('chat-messages'),{path:'/api/chat/attachment?id=long',title:'very-long-file-name-'.repeat(15)+'.pdf'}));
 assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'long filename overflows narrow chat');
 console.log('Attachments: image preview, keyboard/backdrop/Escape, focus restoration, history, uploads, tool images, native downloads, themes and narrow layouts passed');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
