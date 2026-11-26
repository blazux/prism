// Delay child scripts and account preferences: check the pixels before any
// child theme bootstrap can run, then check its eventual palette and identity.
const {chromium} = require('playwright')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const {PNG} = require(path.join(path.dirname(require.resolve('playwright-core/package.json')), 'lib/utilsBundle.js'))
const {createPreferencesBackend} = require('./preferences-fixture.cjs')
const root = path.resolve(__dirname, '..'), origin = 'http://frame-theme.invalid'
const wait = ms => new Promise(resolve => setTimeout(resolve, ms))
const gate = () => { let release; const promise = new Promise(resolve => {release=resolve}); return {promise,release,requested:false} }
const mint = {id:'frame-mint',label:'Frame mint',vars:{'--bg':'#b6d3c9','--bg1':'#c6e1d8','--bg2':'#aac9bb','--text':'#1c3a31','--text2':'#365f4e','--accent':'#2758aa','--border':'#628c75'}}

;(async()=>{
 const browser = await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE_PATH})
 try{
  for(const selected of ['cyberpunk','paper','frame-mint']){
   const context=await browser.newContext({viewport:{width:1100,height:780},reducedMotion:'reduce'}),page=await context.newPage(),errors=[]
   const values={'prism-theme':selected,'prism-custom-themes':JSON.stringify([mint]),'prism-bgfx':'0','active-workspace':'main'}
   const preferences=createPreferencesBackend({origin,values})
   let pending=null
   page.setDefaultTimeout(10000)
   page.on('pageerror',error=>errors.push(error.message))
   await page.addInitScript(()=>{
    window.fixtureSockets=[]
    window.WebSocket=class{static OPEN=1;constructor(){this.readyState=1;fixtureSockets.push(this);queueMicrotask(()=>this.onopen?.())}send(){}close(){this.readyState=3}}
   })
   await context.route('**/*',async route=>{
    const request=route.request(),url=new URL(request.url());assert.equal(url.origin,origin,'first-paint fixture contacted an external service')
    const child=!!request.frame().parentFrame(),blocked=pending
    if(child&&url.pathname==='/theme.js'&&blocked){blocked.script.requested=true;await blocked.script.promise}
    if(child&&url.pathname==='/api/preferences'&&blocked){
     blocked.preferences.requested=true;await blocked.preferences.promise
     // The parent may have a newer unsaved preview than these server values.
     return route.fulfill({contentType:'application/json',body:JSON.stringify({values,owner:'fixture-owner',legacyImportAllowed:false})})
    }
    const pref=preferences.handle(route);if(pref)return pref
    if(url.pathname.startsWith('/api/')){
     let data={}
     if(url.pathname==='/api/auth')data={authenticated:true,auth_enabled:false}
     else if(url.pathname==='/api/me')data={authenticated:true,multiUser:false,isAdmin:true}
     else if(url.pathname==='/api/profile')data={userId:0,displayName:'Synthetic user'}
     else if(url.pathname==='/api/agent/name')data={name:'Synthetic agent'}
     else if(url.pathname==='/api/platform')data={disabledApps:[]}
     else if(url.pathname==='/api/my/groups')data={groups:[]}
     else if(url.pathname==='/api/sessions')data={sessions:[{id:'main',name:'Main'}]}
     else if(url.pathname==='/api/models')data={models:[]}
     else if(url.pathname==='/api/notes')data={notes:[],source:{id:'local',name:'Prism'}}
     return route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
    }
    const file=path.resolve(root,url.pathname==='/'?'index.html':'.'+url.pathname)
    return file.startsWith(root+path.sep)&&fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''})
   })
   async function firstPixels(label){
    const expected=await page.evaluate(()=>{const vars=PrismTheme.currentVars();return [vars['--bg'],vars['--bg1'],vars['--bg2']]})
    const clip=await page.locator('#app-frame').evaluate(element=>{const r=element.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height}})
    assert(clip.width>100&&clip.height>100,'native window must occupy its actual viewport')
    const shot=await page.screenshot({clip,animations:'disabled'}),image=PNG.sync.read(shot)
    const palette=expected.map(hex=>hex.slice(1).match(/../g).map(value=>parseInt(value,16)).join(','))
    for(const[x,y]of[[10,10],[image.width-11,10],[10,image.height-11],[image.width-11,image.height-11]]){
     const index=(y*image.width+x)*4,pixel=[...image.data.subarray(index,index+3)].join(',')
     assert(palette.includes(pixel),'native first paint must use the active theme while its resources are pending ('+selected+'/'+label+': '+pixel+')')
    }
    if(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR){fs.mkdirSync(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR,{recursive:true});fs.writeFileSync(path.join(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR,selected+'-'+label+'.png'),shot)}
   }
   async function requested(block){
    const deadline=Date.now()+5000
    while((!block.script.requested||!block.preferences.requested)&&Date.now()<deadline)await wait(10)
    assert(block.script.requested&&block.preferences.requested,'both child bootstrap and preferences must actually be delayed')
   }
   try{
    await page.goto(origin+'/?workspace=main')
    await page.waitForFunction(()=>fixtureSockets.length>0)
    for(const app of ['settings','notes']){
     await page.evaluate(theme=>PrismTheme.applyTheme(theme),selected)
     pending={script:gate(),preferences:gate()};const block=pending
     try{
      if(app==='settings')await page.locator('#settings-link').click();else await page.locator('.rail-item[data-app="notes"]').click()
      await requested(block)
      await firstPixels(app+'-script-pending')
      block.script.release()
      const element=await page.locator('#app-frame').elementHandle(),frame=await element.contentFrame()
      await frame.waitForFunction(()=>!!window.PrismTheme,null,{polling:20})
      const initial=await page.evaluate(()=>PrismTheme.currentVars())
      assert.equal(await frame.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--bg1').trim()),initial['--bg1'],'child must inherit the parent before its account request resolves')
      await page.evaluate(()=>PrismTheme.previewVars({...PrismTheme.currentVars(),'--bg':'#d8b99f','--bg1':'#efd4b9','--bg2':'#e3c2a4','--text':'#3e281c','--text2':'#72533e','--accent':'#7054a3'}))
      await firstPixels(app+'-preferences-pending-preview')
      block.preferences.release()
      await page.locator('#app-frame').waitFor({state:'visible'})
      const expected=await page.evaluate(()=>PrismTheme.currentVars())
      await frame.waitForFunction(vars=>Object.entries(vars).filter(([key])=>['--bg','--bg1','--text','--accent','color-scheme'].includes(key)).every(([key,value])=>getComputedStyle(document.documentElement).getPropertyValue(key).trim()===value),expected,{polling:20})
      assert.equal(await frame.evaluate(()=>document.documentElement.dataset.theme),'custom-preview','saved defaults must not replace the current unsaved preview')
      assert.equal(await frame.evaluate(()=>PrismPreferences.isReady()),true)
     }finally{block.script.release();block.preferences.release();pending=null}
    }
    assert.deepEqual(errors,[])
    console.log('Native first paint '+selected+': Settings/Notes retain the current palette before scripts, during pending preferences and after unsaved previews.')
   }finally{pending?.script.release();pending?.preferences.release();await context.close()}
  }
 }finally{await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
