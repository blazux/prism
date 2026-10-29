const assert=require('node:assert/strict'),fs=require('fs');
const {chromium}=require('playwright');
const source=fs.readFileSync(require('path').join(__dirname,'../app.js'),'utf8');
function fn(name){const start=source.indexOf('function '+name+'(');const end=source.indexOf('\nfunction ',start+1);return source.slice(start,end<0?undefined:end)}
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try {
 const page=await browser.newPage({viewport:{width:420,height:500}});
 await page.route('http://fixture.invalid/**',route=>{
  if(new URL(route.request().url()).pathname==='/api/chat/attachment')return route.fulfill({headers:{'Content-Type':'application/pdf','Content-Disposition':'attachment; filename=report.pdf'},body:'fixture download'});
  return route.fulfill({contentType:'text/html',body:'<div id="chat-messages" style="max-width:380px"></div>'});
 });
 await page.goto('http://fixture.invalid/');
 await page.addStyleTag({path:require('path').join(__dirname,'../style.css')});
 await page.addScriptTag({path:require('path').join(__dirname,'../theme.js')});
 await page.addScriptTag({content:`let pendingAttachments=[],currentAssistantEl=null,currentAssistantContent='';
 const chatWasAtBottom=()=>true,scrollChat=()=>{},setStreaming=()=>{},renderMarkdown=s=>s,fmtTime=()=>'',userChip=()=>'',agentChip=()=>'',AGENT_NAME='Agent',escHtml=s=>String(s),formatToolInput=()=>'';
 function appendStream(){const box=document.createElement('div');box.className='chat-msg assistant';const body=document.createElement('div');box.appendChild(body);document.getElementById('chat-messages').appendChild(box);currentAssistantEl=body;}
 `+['appendResponseAttachment','finalizeStream','restoreChatHistory'].map(fn).join('\n')});
 await page.evaluate(()=>{pendingAttachments.push({path:'/api/chat/attachment?session=default&id=abc&name=a.txt',title:'a <b>.txt'});finalizeStream()});
 assert.equal(await page.locator('.assistant a .chat-attachment-name').textContent(),'a <b>.txt');
 assert.equal(await page.locator('.chat-attachment-meta').textContent(),'TXT · Download');assert.equal(await page.locator('b').count(),0);
 assert.equal(await page.locator('.assistant a').getAttribute('download'),'a <b>.txt');
 await page.evaluate(()=>{document.getElementById('chat-messages').innerHTML='';restoreChatHistory([{role:'tool',toolName:'add_attachment',content:JSON.stringify({attachment:{name:'report.pdf',url:'/api/chat/attachment?session=default&id=abc'}})}]);appendResponseAttachment(document.body,{path:'javascript:alert(1)',title:'unsafe'})});
 assert.equal(await page.locator('a').count(),1);assert.equal(await page.locator('a').getAttribute('download'),'report.pdf');
 const downloadEvent=page.waitForEvent('download');await page.locator('.chat-attachment-card').click();
 assert.equal((await downloadEvent).suggestedFilename(),'report.pdf');
 await page.evaluate(()=>{document.getElementById('chat-messages').innerHTML='';pendingAttachments.push({images:['YWJj'],path:'/api/chat/attachment?id=abc',title:'image.png'});finalizeStream()});
 assert.equal(await page.locator('img').count(),1);assert.equal(await page.locator('a').count(),1);
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
 console.log('Attachments: file-only reply, escaping, history restoration, URL validation inline images, native downloads, theme changes, keyboard focus and narrow layouts passed');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
