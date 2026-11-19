const {chromium}=require('playwright')
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path')
const root=path.resolve(__dirname,'..'),origin='http://fixture.invalid'

;(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})})
 try{
  const context=await browser.newContext(),page=await context.newPage(),errors=[]
  let streams=0,publications=0
  page.on('pageerror',error=>errors.push(error.message))
  await page.addInitScript(()=>{
   window.fixtureSockets=[]
   window.WebSocket=class{
    static OPEN=1
    constructor(){this.readyState=1;fixtureSockets.push(this);queueMicrotask(()=>this.onopen?.())}
    send(){}
    close(){this.readyState=3}
   }
   window.fixtureDeliver=message=>fixtureSockets.at(-1).onmessage({data:JSON.stringify(message)})
  })
  await page.route('**/*',async route=>{
   const url=new URL(route.request().url())
   assert.equal(url.origin,origin,'fixture requested an external resource')
   if(!url.pathname.startsWith('/api/')){
    const file=path.resolve(root,url.pathname==='/'?'index.html':'.'+url.pathname)
    return file.startsWith(root+path.sep)&&fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''})
   }
   let data={}
   if(url.pathname==='/api/data-events'){
    if(route.request().method()==='GET'){streams++;return route.fulfill({status:500,body:'embedded widgets must use their parent socket'})}
    const body=route.request().postDataJSON();publications++
    assert.equal(url.searchParams.get('session'),'u7-main')
    assert.deepEqual(body,{topic:'todo.shared',scope:'group',group:3})
    await page.evaluate(body=>fixtureDeliver({type:'data_changed',...body,reason:'change'}),body)
    return route.fulfill({contentType:'application/json',body:JSON.stringify({accepted:true})})
   }
   if(url.pathname==='/api/auth')data={authenticated:true,auth_enabled:false}
   else if(url.pathname==='/api/me')data={authenticated:true,multiUser:true,isAdmin:false,user:{id:7,role:'member'}}
   else if(url.pathname==='/api/profile')data={userId:7,displayName:'Fixture'}
   else if(url.pathname==='/api/platform')data={disabledApps:[]}
   else if(url.pathname==='/api/my/groups')data={groups:[{groupId:3,name:'Fixture'}]}
   else if(url.pathname==='/api/agent/name')data={name:'Fixture'}
   else if(url.pathname==='/api/models')data={models:['fixture']}
   else if(url.pathname==='/api/vox-workspace')data={available:false}
   else if(url.pathname==='/api/sessions')data={sessions:[{id:'u7-main',name:'Main'}]}
   else if(url.pathname==='/api/resources')data=[]
   return route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto(origin)
  await page.waitForFunction(()=>fixtureSockets.length>0)
  await page.evaluate(()=>{
   for(let i=0;i<8;i++){
    const host=document.createElement('div'),frame=document.createElement('iframe')
    host.className='widget-body';frame.id='fixture-widget-'+i
    frame.srcdoc=`<script>window.PRISM_SESSION='u7-main';window.reads=1;<\/script><script src="/prism-widget.js"><\/script><script>window.stop=prismOnData(()=>reads++,{topic:'todo.shared',scope:'group',group:3});<\/script>`
    host.appendChild(frame);document.body.appendChild(host)
   }
  })
  await page.waitForFunction(()=>[...document.querySelectorAll('[id^="fixture-widget-"]')].every(frame=>frame.contentWindow.stop))
  assert.equal(streams,0,'eight embedded widgets must not open eight SSE connections')
  const reads=()=>page.evaluate(()=>[...document.querySelectorAll('[id^="fixture-widget-"]')].map(frame=>frame.contentWindow.reads))
  await page.evaluate(()=>document.getElementById('fixture-widget-0').contentWindow.stop())
  await page.evaluate(()=>document.getElementById('fixture-widget-1').contentWindow.prismDataChanged('todo.shared',{scope:'group',group:3}))
  await page.waitForFunction(()=>document.getElementById('fixture-widget-7').contentWindow.reads===2)
  assert.deepEqual(await reads(),[1,2,2,2,2,2,2,2]);assert.equal(publications,1)
  await page.evaluate(()=>fixtureDeliver({type:'data_changed',topic:'unrelated.topic',scope:'group',group:3}))
  assert.deepEqual(await reads(),[1,2,2,2,2,2,2,2])
  await page.evaluate(()=>fixtureSockets.at(-1).onopen())
  await page.waitForFunction(()=>document.getElementById('fixture-widget-7').contentWindow.reads===3)
  assert.deepEqual(await reads(),[1,3,3,3,3,3,3,3])
  assert.equal(streams,0);assert.equal(publications,1);assert.deepEqual(errors,[])
  await context.close()
  console.log('Data events: eight widgets share the dashboard socket; custom publication, topic filters, unsubscribe and reconnect refresh passed')
 }finally{await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
