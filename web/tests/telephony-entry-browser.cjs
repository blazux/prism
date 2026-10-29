const {chromium}=require('playwright');
const fs=require('fs'),path=require('path');
const root=path.resolve(__dirname,'..');
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE_PATH});
 try {
  const page=await browser.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{
   localStorage.setItem('active-workspace','default');
   window.__sockets=[];
   window.WebSocket=class {static OPEN=1;constructor(url){this.url=url;this.readyState=0;window.__sockets.push(this)}close(){this.readyState=3}send(){}};
  });
  await page.route('**/*',route=>{
   const u=new URL(route.request().url());
   if(u.hostname!=='fixture.invalid')return route.abort();
   let data={};
   if(u.pathname==='/api/auth')data={authenticated:true};
   else if(u.pathname==='/api/vox-workspace')data={available:true,admin:true,groups:[]};
   else if(u.pathname==='/api/me')data={isAdmin:true,authenticated:true};
   else if(u.pathname==='/api/sessions')data={sessions:[{id:'default',name:'Default'}]};
   else if(u.pathname==='/api/models')data={models:[]};
   else if(u.pathname==='/api/my/groups')data={groups:[]};
   if(u.pathname.startsWith('/api/'))return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
   const file=path.join(root,u.pathname==='/'?'index.html':u.pathname);
   if(fs.existsSync(file)&&fs.statSync(file).isFile())return route.fulfill({path:file});
   return route.fulfill({status:404,body:''});
  });
  await page.goto('http://fixture.invalid/?app=telephony');
  await page.waitForFunction(()=>window.__sockets.length>=1,{},{timeout:10000});
  const sockets=await page.evaluate(()=>window.__sockets.map(s=>({url:s.url,closed:s.readyState===3})));
  console.log(JSON.stringify({sockets,errors}));
  if(sockets.length!==1||sockets.some(s=>s.closed)||errors.length)throw new Error('Telephony entry duplicated or broke the chat connection');
  await page.locator('[data-board-id=default]').click();
  const switched=await page.evaluate(()=>window.__sockets.map(s=>({url:s.url,closed:s.readyState===3})));
  if(switched.length!==2||!switched[0].closed||switched[1].closed)throw new Error('Workspace switch did not replace the socket');
  console.log('Telephony deep link uses one socket and workspace navigation replaces it');
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
