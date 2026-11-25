const {createPreferencesBackend} = require('./preferences-fixture.cjs')
const {chromium}=require('playwright'),assert=require('node:assert/strict'),fs=require('fs'),path=require('path');
const root=path.resolve(__dirname,'..');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})});
 try{
  for(const multi of [false,true]){
   const page=await browser.newPage({locale:'en-US'}),errors=[];page.on('pageerror',error=>errors.push(error.message));
   await page.addInitScript(()=>{window.__sockets=[];window.WebSocket=class{static OPEN=1;constructor(url){this.url=url;this.frames=[];this.readyState=0;window.__sockets.push(this)}close(){this.readyState=3}send(frame){this.frames.push(JSON.parse(frame))}}});
   const preferencesFixture = createPreferencesBackend({})
   await page.route('**/*',route=>{
     const preferenceResponse = preferencesFixture.handle(route)
     if (preferenceResponse) return preferenceResponse
    const url=new URL(route.request().url());assert.equal(url.hostname,'fixture.invalid');
    if(!url.pathname.startsWith('/api/')){const file=path.join(root,url.pathname==='/'?'index.html':url.pathname);if(fs.existsSync(file)&&fs.statSync(file).isFile())return route.fulfill({path:file});return route.fulfill({status:404,body:''});}
    let data={};
    if(url.pathname==='/api/auth')data={authenticated:true};
    else if(url.pathname==='/api/me')data={authenticated:true,isAdmin:true,multiUser:multi};
    else if(url.pathname==='/api/profile')data={userId:1,displayName:'Fixture'};
    else if(url.pathname==='/api/sessions')data={sessions:[{id:'default',name:'Default'}]};
    else if(url.pathname==='/api/vox-workspace')data={available:true,admin:false,multiUser:multi,groups:multi?[{id:2,name:'Operations'}]:[]};
    else if(url.pathname==='/api/my/groups')data={groups:multi?[{id:2,name:'Operations'}]:[]};
    else if(url.pathname==='/api/vox-workspace/calls')data={total:0,items:[]};
    else if(url.pathname==='/api/vox-workspace/history')data={total:1,items:[{id:8,caller_number:'+15550123',direction:'inbound',started_at:'2026-11-01T09:00:00Z',ended_at:'2026-11-01T09:01:00Z'}],next_offset:null};
    return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
   });
   await page.goto('http://fixture.invalid/?app=telephony');
   const app=page.frameLocator('#app-frame');await app.locator('#askAgent').waitFor();
   assert.notEqual(await page.locator('#chat-fab').evaluate(element=>getComputedStyle(element).display),'none');
   if(multi){
    await app.locator('#outboundTab').click();await app.locator('#askAgent').click();
    await page.waitForFunction(()=>document.getElementById('chat-input').value.includes('my outbound missions'));
    assert.ok((await page.locator('#app-frame').getAttribute('src')).includes('telephony.html'));
    await page.locator('#chat-input').fill('');await app.locator('#logsTab').click();
   }
   await app.locator('#calls tr.call').click();await app.locator('#askAgent').click();
   if(multi){
    await page.waitForFunction(()=>document.getElementById('app-frame').src.includes('/apps/room.html?'));
    const room=page.frameLocator('#app-frame');await room.locator('#box').filter({visible:true}).waitFor();
    await page.waitForFunction(()=>document.getElementById('app-frame').contentDocument?.getElementById('box')?.value.startsWith('@agent '));
    assert.equal(await room.locator('#group').inputValue(),'2');assert.ok((await room.locator('#box').inputValue()).includes('"id":8'));
    assert.equal(await page.evaluate(()=>document.getElementById('app-frame').contentWindow.__sockets.some(socket=>socket.frames.some(frame=>frame.type==='send'))),false);
   }else{
    await page.waitForFunction(()=>document.getElementById('chat-input').value.includes('"id":8'));
    assert.ok((await page.locator('#chat-input').inputValue()).includes('"id":8'));
    assert.equal(await page.evaluate(()=>window.__sockets.some(socket=>socket.frames.some(frame=>frame.type==='chat'))),false);
   }
   assert.deepEqual(errors,[]);await page.close();
  }
  console.log('Telephony handoff targets the personal agent or the selected authorized Room, preserving a draft without sending');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});
