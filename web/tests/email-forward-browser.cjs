// Native mail composer with synthetic messages, downloads and draft persistence.
// Every request is intercepted; this never connects to a mailbox or sends mail.
const {chromium}=require('playwright');
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'..'),origin='http://127.0.0.1:4893',account='fixture-mailbox';
const original={uid:17,folder:'INBOX',from:'No reply <noreply@example.test>',replyTo:'support@example.test',to:'me@example.test, teammate@example.test',cc:'team@example.test',subject:'Résumé du dossier',date:'2026-11-26T10:00:00Z',seen:false,body:'Bonjour, voici le dossier avec ses deux pièces jointes.',attachments:[{filename:'résumé.txt',contentType:'text/plain',size:40},{filename:'octets.bin',contentType:'application/octet-stream',size:4}]};
const bytes=[Buffer.from('Pièce jointe déjà vérifiée — résumé.'),Buffer.from([0,255,13,10])];
;(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage();
  const errors=[],drafts=new Map(),downloads=[];let nextID=1,sendRequests=0,hold=true,releaseSecond,failSecond=false;
  page.on('pageerror',error=>errors.push(error.message));
  await context.route('**/*',async route=>{
   const request=route.request(),url=new URL(request.url());
   assert.equal(url.origin,origin,'Mail fixture must not contact another origin');
   const reply=(value,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(value)});
   if(!url.pathname.startsWith('/api/')){
    const file=path.join(root,url.pathname);
    return fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:'Not found'});
   }
   if(url.pathname==='/api/preferences')return reply({owner:'fixture-owner',legacyImportAllowed:false,values:{}});
   if(url.pathname==='/api/email/config')return reply({configured:true,has_password:true,user:'me@example.test',from:'me@example.test',list_limit:60,account});
   if(url.pathname==='/api/email/list')return reply({account,messages:[original],total:1,folder_total:1,unread_count:original.seen?0:1,limit:60,offset:0,next_offset:null});
   if(url.pathname==='/api/email/status')return reply({account,folder:'INBOX',messages:1,unread:original.seen?0:1,uid_next:18,uid_validity:123});
   if(url.pathname==='/api/email/tags')return reply({});
   if(url.pathname==='/api/email/markseen'){original.seen=request.postDataJSON().seen;return reply({ok:true});}
   if(url.pathname==='/api/email/read')return reply({account,message:original});
   if(url.pathname==='/api/builtin/email')return reply({result:JSON.stringify([{name:'INBOX',role:'inbox',selectable:true,total:1,unread:original.seen?0:1}])});
   if(url.pathname==='/api/email/attachment'){
    assert.equal(url.searchParams.get('folder'),'INBOX');assert.equal(url.searchParams.get('uid'),'17');assert.equal(url.searchParams.get('account'),account);
    const index=Number(url.searchParams.get('i'));downloads.push(index);
    if(index===1&&hold)await new Promise(resolve=>releaseSecond=resolve);
    if(index===1&&failSecond)return reply({error:'Download temporarily unavailable'},503);
    return route.fulfill({contentType:'application/octet-stream',body:bytes[index]});
   }
   if(url.pathname==='/api/email/drafts'){
    const metadata=draft=>{const {attachments,...rest}=draft;return {...rest,attachmentCount:attachments?.length||0};};
    if(request.method()==='GET'){
     if(url.searchParams.has('id'))return reply({draft:drafts.get(Number(url.searchParams.get('id'))),mailboxChanged:false});
     return reply({drafts:[...drafts.values()].map(metadata)});
    }
    assert.equal(request.method(),'POST');const input=request.postDataJSON();assert.equal(input.account,account);
    const id=input.id||nextID++,prior=drafts.get(id)||{revision:0};
    const draft={...prior,...input,id,revision:prior.revision+1,status:'draft',updatedAt:'2026-11-26T10:00:00Z'};drafts.set(id,draft);
    return reply({draft:metadata(draft)});
   }
   if(url.pathname==='/api/email/send'){sendRequests++;return reply({error:'Sending is forbidden in this fixture'},500);}
   return reply({});
  });
  await page.goto(origin+'/apps/email.html');await page.locator('.mrow').first().waitFor();
  await page.locator('.mrow').first().click();await page.locator('.reading').getByRole('button',{name:'Reply',exact:true}).waitFor();
  await page.locator('.reading').getByRole('button',{name:'Reply',exact:true}).click();
  assert.equal(await page.locator('#c-to').inputValue(),'support@example.test');
  await page.evaluate(()=>closeCompose(true));
  await page.locator('.reading').getByRole('button',{name:'Reply all',exact:true}).click();
  assert.equal(await page.locator('#c-to').inputValue(),'support@example.test, teammate@example.test');
  assert.equal(await page.locator('#c-cc').inputValue(),'team@example.test');
  await page.evaluate(()=>closeCompose(true));
  await page.evaluate(()=>{window.pendingForward=forward();});
  await page.locator('#overlay.open').waitFor();await page.locator('#c-to').fill('recipient@example.test');
  await page.locator('#c-body').fill('Ma note personnelle, avec les originaux.');
  await page.waitForFunction(()=>document.getElementById('c-status').textContent.includes('(1/2)'),null,{polling:50});
  assert(await page.locator('#send-mail').isDisabled());assert.equal(drafts.size,0,'An incomplete forward must not become a saved draft');
  await page.evaluate(()=>doSend());assert.equal(sendRequests,0);
  assert.equal(await page.evaluate(()=>composeAtts.length),0,'Partial downloads must not appear as a complete forward');
  hold=false;releaseSecond();assert.equal(await page.evaluate(()=>pendingForward),true);
  await page.evaluate(()=>flushDraft());
  const saved=[...drafts.values()].at(-1);assert.equal(saved.body,'Ma note personnelle, avec les originaux.');assert.equal(saved.attachments.length,2);
  assert.equal(saved.attachments[0].filename,'résumé.txt');assert.equal(saved.attachments[0].content_type,'text/plain');
  assert.deepEqual(Buffer.from(saved.attachments[0].data,'base64'),bytes[0]);assert.deepEqual(Buffer.from(saved.attachments[1].data,'base64'),bytes[1]);
  await page.evaluate(()=>closeCompose(true));failSecond=true;
  await page.evaluate(()=>{window.pendingForward=forward();});assert.equal(await page.evaluate(()=>pendingForward),false);
  assert(await page.locator('#send-mail').isDisabled());assert(await page.locator('#forward-retry').isVisible());
  const before=drafts.size;await page.evaluate(()=>doSend());assert.equal(sendRequests,0);assert.equal(drafts.size,before);
  assert.equal(await page.evaluate(()=>composeAtts.length),0);
  failSecond=false;await page.locator('#forward-retry').click();
  await page.waitForFunction(()=>forwardState?.complete,null,{polling:50});await page.evaluate(()=>flushDraft());
  assert.equal(await page.evaluate(()=>composeAtts.length),2);assert.equal(drafts.size,before+1);assert.equal([...drafts.values()].at(-1).attachments.length,2);
  assert.deepEqual(errors,[]);assert.equal(sendRequests,0);
  console.log('Mail reply/forward: Reply-To, real binary FileReader drafts, pending/failed attachment gates and retry passed without sending mail.');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
