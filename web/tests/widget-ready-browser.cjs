// The actual dashboard restores authored widgets through the public bootstrap,
// document.write, CSS, fonts and load events. Every request is synthetic.
const {chromium} = require('playwright')
const {createPreferencesBackend} = require('./preferences-fixture.cjs')
const {PNG} = require(require('node:path').join(require('node:path').dirname(require.resolve('playwright-core/package.json')), 'lib/utilsBundle.js'))
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const root = path.resolve(__dirname, '..'), mainOrigin = 'http://127.0.0.1:49080', contentOrigin = 'http://127.0.0.1:49082'
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
function gate() { let release; const promise = new Promise(resolve => { release = resolve }); return {promise, release} }
function stage() {
  const state = {base:gate(), css:gate(), image:gate(), fonts:{alpha:gate(), beta:gate()}, requests:{base:0,css:new Set(),image:new Set(),font:new Set()}}
  state.release = () => { state.base.release(); state.css.release(); state.image.release(); Object.values(state.fonts).forEach(item => item.release()) }
  return state
}
async function until(predicate, label) { const end=Date.now()+7000; while(!predicate()&&Date.now()<end) await delay(10); assert(predicate(),label) }
function authored(id) {
  return `<!doctype html><html><head><link rel="stylesheet" href="/data/${id}.css">${id==='alpha'?'<script src="/theme.js"><\/script>':''}</head><body><main class="fixture-ready" id="fixture"><strong>${id}</strong><p>A complete authored widget</p><img src="/data/${id}.svg" alt="Synthetic preview"></main><script>window.fixtureNonce=Math.random();document.fonts.load('20px FlashFixture').then(()=>document.documentElement.dataset.fixtureFont='ready');document.addEventListener('DOMContentLoaded',()=>document.documentElement.dataset.fixtureDom='ready');<\/script></body></html>`
}

;(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})})
  try {
    for (const theme of ['cyberpunk','paper','custom-mint']) {
      const multi=theme==='paper', session=multi?'u7-main':'main'
      const context=await browser.newContext({viewport:{width:1200,height:850},reducedMotion:'reduce'}), page=await context.newPage()
      let pending=stage(), failAuthorize=false, grants=0, heldGrant=null
      const errors=[], heldGrants=[], paletteKeys=['--bg1','--text','--text2','--accent','--border','color-scheme']
      const definitions=['alpha','beta'].map((id,index)=>({type:'plugin_load',id,title:'Fixture '+id,content:authored(id),open:true,x:20+index*500,y:20,w:460,h:310}))
      const preferences=createPreferencesBackend({origin:mainOrigin,values:{'active-workspace':session,'prism-theme':theme,'prism-bgfx':'0',
        'prism-custom-themes':JSON.stringify([{id:'custom-mint',label:'Custom mint',vars:{'--bg':'#b6d3c9','--bg1':'#c6e1d8','--text':'#1c3a31','--text2':'#365f4e','--accent':'#2758aa','--border':'#628c75'}}])}})
      page.setDefaultTimeout(12000)
      page.on('pageerror',error=>errors.push(error.message))
      await context.addInitScript(({definitions})=>{
        window.fixtureSockets=[]
        window.WebSocket=class {
          static OPEN=1
          constructor(url){this.url=url;this.readyState=1;this.messages=[];fixtureSockets.push(this);queueMicrotask(()=>{this.onopen?.();setTimeout(()=>{if(this===fixtureSockets.at(-1))for(const item of definitions)this.onmessage?.({data:JSON.stringify(item)})},0)})}
          send(value){this.messages.push(JSON.parse(value))} close(){this.readyState=3}
        }
        window.fixtureDeliver=item=>fixtureSockets.at(-1).onmessage({data:JSON.stringify(item)})
      },{definitions})
      await context.route('**/*',async route=>{
        const request=route.request(), url=new URL(request.url()), response=preferences.handle(route)
        if(response)return response
        assert([mainOrigin,contentOrigin].includes(url.origin),'Widget fixture contacted an external service')
        if(url.origin===contentOrigin){
          if(url.pathname==='/__prism_widget/authorize')return route.fulfill({status:failAuthorize?401:200,contentType:'application/json',body:failAuthorize?'{}':'{"ok":true}'})
          if(url.pathname==='/widget-base.css'){const current=pending;current.requests.base++;await current.base.promise}
          const asset=url.pathname.match(/^\/data\/(alpha|beta)\.(css|woff2|svg)$/)
          if(asset){
            const [,id,kind]=asset,current=pending
            if(kind==='css'){
              current.requests.css.add(id);await current.css.promise
              return route.fulfill({contentType:'text/css',headers:{'cache-control':'no-store'},body:`@font-face{font-family:FlashFixture;src:url('/data/${id}.woff2') format('woff2');font-display:block}.fixture-ready{display:grid;font-family:FlashFixture,serif;font-size:20px;gap:10px;background:var(--bg2);padding:16px;border:2px solid var(--accent)}.fixture-ready img{width:40px;height:20px}`})
            }
            if(kind==='woff2'){current.requests.font.add(id);await current.fonts[id].promise;return route.fulfill({path:path.join(root,'vendor/katex/fonts/KaTeX_Main-Regular.woff2'),headers:{'cache-control':'no-store'}})}
            current.requests.image.add(id);await current.image.promise
            return route.fulfill({contentType:'image/svg+xml',headers:{'cache-control':'no-store'},body:'<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="#759db5"/></svg>'})
          }
          if(url.pathname.startsWith('/api/'))return route.fulfill({status:403,body:'Not a fixture widget API'})
        }else if(url.pathname.startsWith('/api/')){
          let value={}
          if(url.pathname==='/api/auth')value={authenticated:true}
          else if(url.pathname==='/api/me')value={authenticated:true,multiUser:multi,isAdmin:true,user:multi?{id:7,role:'global_admin'}:undefined,widgetHosting:{frameURL:contentOrigin+'/widget-frame.html',grantURL:'/api/widget/grant'}}
          else if(url.pathname==='/api/widget/grant'){
            assert.equal(request.postDataJSON().session,session);value={token:'fixture-grant-'+(++grants)}
            if(heldGrant){
              const held=heldGrant;heldGrant=null;held.started=true
              await held.promise
              await route.fulfill({contentType:'application/json',body:JSON.stringify(value)})
              held.finished.release();return
            }
          }
          else if(url.pathname==='/api/profile')value={userId:multi?7:0,displayName:'Fixture'}
          else if(url.pathname==='/api/sessions')value={sessions:[{id:session,name:'Main'}]}
          else if(url.pathname==='/api/my/groups')value={groups:[]}
          else if(url.pathname==='/api/models')value={models:['fixture']}
          else if(url.pathname==='/api/platform')value={disabledApps:[]}
          else if(url.pathname==='/api/agent/name')value={name:'Fixture'}
          else if(url.pathname==='/api/vox-workspace')value={available:false}
          else if(url.pathname==='/api/resources')value=[]
          return route.fulfill({contentType:'application/json',body:JSON.stringify(value)})
        }
        const file=path.resolve(root,url.pathname==='/'?'index.html':'.'+url.pathname)
        return file.startsWith(root+path.sep)&&fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file,headers:{'cache-control':'no-store'}}):route.fulfill({status:404,body:''})
      })
      const shell=id=>page.locator('#widget-'+id+' .widget-body')
      const frame=async id=>(await shell(id).locator('iframe').elementHandle()).contentFrame()
      async function pixels(id,label){
        const image=PNG.sync.read(await shell(id).screenshot()), expected=await page.evaluate(()=>PrismTheme.currentVars()['--bg1'])
        const rgb=expected.slice(1).match(/../g).map(value=>parseInt(value,16))
        for(const [x,y]of [[3,3],[image.width-4,3],[3,image.height-4],[image.width-4,image.height-4]]){
          const index=(y*image.width+x)*4;assert.deepEqual([...image.data.subarray(index,index+3)],rgb,label+' must keep the active background')
        }
        if(process.env.PRISM_WIDGET_FLASH_ARTIFACTS){fs.mkdirSync(process.env.PRISM_WIDGET_FLASH_ARTIFACTS,{recursive:true});fs.writeFileSync(path.join(process.env.PRISM_WIDGET_FLASH_ARTIFACTS,theme+'-'+id+'-'+label+'.png'),PNG.sync.write(image))}
      }
      async function loading(id,label){
        assert.equal(await shell(id).locator(':scope > .widget-loader').isVisible(),true,label+': the parent loader disappeared before the authored document was ready')
        assert.equal(await shell(id).locator('iframe').evaluate(el=>getComputedStyle(el).visibility),'hidden',label+': the incomplete authored document was revealed')
        await pixels(id,label)
      }
      async function ready(id){
        await shell(id).locator('iframe').waitFor({state:'visible'})
        assert.equal(await shell(id).locator(':scope > .widget-loader').isVisible(),false)
        const content=await frame(id)
        await content.locator('#fixture').waitFor()
        assert.equal(await content.locator('#fixture').evaluate(el=>getComputedStyle(el).display),'grid')
        const expected=await page.evaluate(keys=>Object.fromEntries(keys.map(key=>[key,PrismTheme.currentVars()[key]])),paletteKeys)
        await content.waitForFunction(expected=>Object.entries(expected).every(([key,value])=>getComputedStyle(document.documentElement).getPropertyValue(key).trim()===value),expected,{polling:50})
        assert.equal(await content.evaluate(()=>document.documentElement.dataset.fixtureFont),'ready')
        return content
      }
      try{
        await page.goto(mainOrigin+'/?workspace='+session)
        await until(()=>pending.requests.base>=2,'Both restored widgets requested the base CSS')
        await loading('alpha','base-css');await loading('beta','base-css')
        pending.base.release()
        await until(()=>pending.requests.css.size===2,'Both authored stylesheets were requested')
        await loading('alpha','authored-css');await loading('beta','authored-css')
        pending.css.release()
        await until(()=>pending.requests.font.size===2&&pending.requests.image.size===2,'Both widgets requested their fonts and images')
        await loading('alpha','assets');await loading('beta','assets')
        pending.image.release();pending.fonts.alpha.release()
        const alpha=await ready('alpha')
        await loading('beta','independent-font')
        await page.evaluate(()=>PrismTheme.previewVars({...PrismTheme.currentVars(),'--bg1':'#29483e','--text':'#f7f5ed','--text2':'#b9cfdf','--accent':'#73d8bc'}))
        await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
        await loading('beta','late-preview')
        pending.fonts.beta.release();await ready('beta');await ready('alpha')
        await page.evaluate(()=>{window.restoredFrames=[...document.querySelectorAll('.widget-body iframe')];})
        const before=grants, nonce=await alpha.evaluate(()=>fixtureNonce)
        await page.evaluate(definitions=>definitions.forEach(item=>fixtureDeliver(item)),definitions)
        await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
        assert.equal(await page.evaluate(()=>window.restoredFrames.every((frame,index)=>frame===document.querySelectorAll('.widget-body iframe')[index])),true,'Identical WS restoration remounted already ready widgets')
        assert.equal(grants,before);assert.equal(await alpha.evaluate(()=>fixtureNonce),nonce)

        pending=stage()
        await page.reload({waitUntil:'domcontentloaded'})
        await until(()=>pending.requests.base>=2,'Reload restored both widgets through their base CSS')
        await loading('alpha','reload');await loading('beta','reload')
        pending.release();await ready('alpha');await ready('beta')

        const held=gate();held.finished=gate();heldGrant=held;heldGrants.push(held)
        const beforeRemount=grants
        await page.evaluate(item=>fixtureDeliver(item),{...definitions[0],id:'remounted',title:'Remounted fixture',x:20,y:400})
        await until(()=>held.started,'The original grant was held in flight')
        await page.locator('#widget-remounted .widget-min:not(.widget-share)').click()
        await page.getByRole('button',{name:'Remounted fixture',exact:true}).click()
        await until(()=>grants===beforeRemount+2,'Restoring a frame must request its own grant while the detached frame is still waiting')
        const remounted=await ready('remounted'), remountedNonce=await remounted.evaluate(()=>fixtureNonce)
        held.release();await held.finished.promise
        await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
        assert.equal(await remounted.evaluate(()=>fixtureNonce),remountedNonce,'The detached frame grant replaced its ready successor')

        failAuthorize=true
        await page.evaluate(item=>fixtureDeliver(item),{...definitions[0],id:'failed',title:'Failed fixture',x:520,y:400})
        await shell('failed').locator('.widget-loader-status').filter({hasText:'could not be opened'}).waitFor()
        assert.equal(await shell('failed').locator('iframe').evaluate(el=>getComputedStyle(el).visibility),'hidden')
        assert.equal(await (await frame('failed')).evaluate(()=>typeof fixtureNonce),'undefined')
        await pixels('failed','error')
        assert.deepEqual(errors,[])
        console.log('Widget lifecycle '+theme+': base CSS, authored CSS, image/font gates, final palette, independent readiness, WS replay, reload, detached grants and themed failure passed.')
      }finally{pending.release();heldGrants.forEach(item=>item.release());await context.close()}
    }
  }finally{await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
