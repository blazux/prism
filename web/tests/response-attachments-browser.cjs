const assert=require('node:assert/strict'),fs=require('fs');
const {chromium}=require('playwright');
const source=fs.readFileSync(require('path').join(__dirname,'../app.js'),'utf8');
function fn(name){const start=source.indexOf('function '+name+'(');const end=source.indexOf('\nfunction ',start+1);return source.slice(start,end<0?undefined:end)}
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try {
 const page=await browser.newPage();
 await page.setContent('<div id="chat-messages"></div>');
 await page.addScriptTag({content:`let pendingAttachments=[],currentAssistantEl=null,currentAssistantContent='';
 const chatWasAtBottom=()=>true,scrollChat=()=>{},setStreaming=()=>{},renderMarkdown=s=>s,fmtTime=()=>'',userChip=()=>'',agentChip=()=>'',AGENT_NAME='Agent',escHtml=s=>String(s),formatToolInput=()=>'';
 function appendStream(){const box=document.createElement('div');box.className='chat-msg assistant';const body=document.createElement('div');box.appendChild(body);document.getElementById('chat-messages').appendChild(box);currentAssistantEl=body;}
 `+['appendResponseAttachment','finalizeStream','restoreChatHistory'].map(fn).join('\n')});
 await page.evaluate(()=>{pendingAttachments.push({path:'/api/chat/attachment?session=default&id=abc&name=a.txt',title:'a <b>.txt'});finalizeStream()});
 assert.equal(await page.locator('.assistant a').textContent(),'↓ a <b>.txt');assert.equal(await page.locator('b').count(),0);
 assert.equal(await page.locator('.assistant a').getAttribute('download'),'a <b>.txt');
 await page.evaluate(()=>{document.getElementById('chat-messages').innerHTML='';restoreChatHistory([{role:'tool',toolName:'add_attachment',content:JSON.stringify({attachment:{name:'report.pdf',url:'/api/chat/attachment?session=default&id=abc'}})}]);appendResponseAttachment(document.body,{path:'javascript:alert(1)',title:'unsafe'})});
 assert.equal(await page.locator('a').count(),1);assert.equal(await page.locator('a').getAttribute('download'),'report.pdf');
 await page.evaluate(()=>{document.getElementById('chat-messages').innerHTML='';pendingAttachments.push({images:['YWJj'],path:'/api/chat/attachment?id=abc',title:'image.png'});finalizeStream()});
 assert.equal(await page.locator('img').count(),1);assert.equal(await page.locator('a').count(),1);
 console.log('Attachments: file-only reply, escaping, history restoration, URL validation and inline images passed');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
