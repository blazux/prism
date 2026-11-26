const {createPreferencesBackend} = require('./preferences-fixture.cjs')
const {chromium} = require('playwright')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const {PNG} = require(path.join(path.dirname(require.resolve('playwright-core/package.json')), 'lib/utilsBundle.js'))
const root = path.resolve(__dirname, '..')
const mainOrigin = 'http://127.0.0.1:48080', widgetOrigin = 'http://127.0.0.1:48082', serviceOrigin = 'http://service.invalid'
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=','base64')

;(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.CHROMIUM_EXECUTABLE_PATH ? {executablePath:process.env.CHROMIUM_EXECUTABLE_PATH} : {})})
  try {
    for (const multi of [false, true]) {
      const context = await browser.newContext({viewport:{width:1200,height:850}}), page = await context.newPage()
      const session = multi ? 'u7-main' : 'main', errors = [], tools = [], grants = []
      let authorizations = 0, expired = false, identityUnavailable = false, failAuthorize = false, failGrant = false, authorizeGate = null, frameScriptGate = null
      page.setDefaultTimeout(10000)
      page.on('pageerror', error => errors.push(error.message))
      await context.addCookies([{name:'prism_session',value:'synthetic-dashboard-login',url:mainOrigin,httpOnly:true,sameSite:'Strict'}])
      await context.addInitScript(({mainOrigin}) => {
        if (location.origin !== mainOrigin) return
        localStorage.setItem('fixture-widget-choice','ancien choix é')
        localStorage.setItem('agent-config','private dashboard preference')
        sessionStorage.setItem('fixture-widget-tab','current tab é')
        window.fixtureSockets = []
        window.WebSocket = class {
          static OPEN=1
          constructor(url) {this.url=url;this.readyState=1;this.messages=[];fixtureSockets.push(this);queueMicrotask(()=>this.onopen?.())}
          send(value) {this.messages.push(JSON.parse(value))}
          close() {this.readyState=3}
        }
        window.fixtureDeliver = message => fixtureSockets.at(-1).onmessage({data:JSON.stringify(message)})
      },{mainOrigin})
      await context.routeWebSocket(widgetOrigin.replace('http:','ws:')+'/proxy/fixture/80/ws', socket => {
        socket.onMessage(message => {if(message==='ping')socket.send('pong')})
      })
      const preferencesFixture = createPreferencesBackend({origin: mainOrigin, values:{'prism-theme':multi?'paper':'cyberpunk'}})
      await context.route('**/*', async route => {
        const preferenceResponse = preferencesFixture.handle(route)
        if (preferenceResponse) return preferenceResponse
        const request = route.request(), url = new URL(request.url())
        if (url.origin === serviceOrigin) return route.fulfill({contentType:'text/html',body:'<!doctype html><body>Nested app<script>localStorage.setItem("nested-app","saved");document.body.dataset.working=localStorage.getItem("nested-app")</script>'})
        assert([mainOrigin,widgetOrigin].includes(url.origin),'unexpected external request: '+url.href)
        if (url.origin === widgetOrigin) {
          if (url.pathname === '/widget-frame.js' && frameScriptGate) await frameScriptGate
          if (url.pathname === '/__prism_widget/authorize') {
            authorizations++
            assert.match(request.postDataJSON().token,/^fixture-grant-/)
            if (authorizeGate) await authorizeGate
            if (failAuthorize) return route.fulfill({status:401,body:'Widget access unavailable'})
            expired=false
            return route.fulfill({status:200,headers:{'set-cookie':'prism_widget=synthetic-content-login; Path=/; HttpOnly; SameSite=Strict'},contentType:'application/json',body:'{"ok":true}'})
          }
          if (url.pathname.startsWith('/api/builtin/') || url.pathname.startsWith('/api/tool/')) {
            if(expired)return route.fulfill({status:401,body:'Widget access expired'})
            assert(request.headers().cookie?.includes('prism_widget=synthetic-content-login'),'tool request must have content credential')
            assert.equal(url.searchParams.get('session'),session)
            tools.push(url.pathname)
            return route.fulfill({contentType:'application/json',body:JSON.stringify({result:{answer:'tool-é'},images:[]})})
          }
          if (url.pathname === '/api/data-events') return route.fulfill({contentType:'text/event-stream',body:'data: direct stream\n\n'})
          if (url.pathname === '/data/fixture.json') return route.fulfill({contentType:'application/json',body:'{"answer":"data-é"}'})
          if (url.pathname === '/data/pixel.png') return route.fulfill({contentType:'image/png',body:png})
          if (url.pathname === '/data/export.txt') return route.fulfill({headers:{'content-disposition':'attachment; filename="fixture-export.txt"'},contentType:'text/plain',body:'Fichier é'})
          if (url.pathname.startsWith('/api/')) return route.fulfill({status:403,body:'Not a widget route'})
        } else if (url.pathname.startsWith('/api/')) {
          let data = {}
          if(url.pathname==='/api/auth')data={authenticated:true,auth_enabled:true}
          else if(url.pathname==='/api/me') {
            if(identityUnavailable)return route.fulfill({status:503,contentType:'application/json',body:'{}'})
            data={authenticated:true,multiUser:multi,isAdmin:true,user:multi?{id:7,role:'global_admin'}:undefined,widgetHosting:{frameURL:widgetOrigin+'/widget-frame.html?parent='+encodeURIComponent(mainOrigin),grantURL:'/api/widget/grant'}}
          } else if(url.pathname==='/api/widget/grant') {
            if(failGrant)return route.fulfill({status:503,body:'Widget access unavailable'})
            assert.equal(request.postDataJSON().session,session)
            const token='fixture-grant-'+(grants.length+1);grants.push(token);data={token}
          } else if(url.pathname==='/api/admin/platform')data={private:'dashboard-only'}
          else if(url.pathname==='/api/profile')data={userId:multi?7:0,displayName:'Fixture'}
          else if(url.pathname==='/api/platform')data={disabledApps:[]}
          else if(url.pathname==='/api/my/groups')data={groups:[]}
          else if(url.pathname==='/api/sessions')data={sessions:[{id:session,name:'Main'}]}
          else if(url.pathname==='/api/models')data={models:['fixture']}
          else if(url.pathname==='/api/agent/name')data={name:'Fixture'}
          else if(url.pathname==='/api/vox-workspace')data={available:false}
          else if(url.pathname==='/api/resources')data=[]
          return route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
        }
        const file=path.resolve(root,url.pathname==='/'?'index.html':'.'+url.pathname)
        return file.startsWith(root+path.sep)&&fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''})
      })
      await page.goto(mainOrigin+'/?workspace='+session)
      await page.waitForFunction(()=>fixtureSockets.length>0)
      const content = `<!doctype html><html><head></head><body><p id="state">Opening</p><img id="pixel" src="/data/pixel.png"><a id="export" href="/data/export.txt" download>Download</a><iframe id="service" src="${serviceOrigin}/"></iframe><script>
        window.state={events:0,nonce:Math.random(),legacyLocal:localStorage.getItem('fixture-widget-choice'),legacySession:sessionStorage.getItem('fixture-widget-tab'),privatePreference:localStorage.getItem('agent-config')};
        try{parent.document.body;state.parentBlocked=false}catch(_){state.parentBlocked=true}
        localStorage.setItem('fixture-widget-choice','nouveau choix é');sessionStorage.setItem('fixture-widget-tab','updated tab é');
        prismOnData(()=>state.events++,{topic:'fixture'});
        Promise.all([prismTool('fixture'),prismToolRaw('fixture-raw'),fetch('/data/fixture.json').then(r=>r.json()),fetch('/api/tool/legacy?session='+encodeURIComponent(PRISM_SESSION),{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.json()),new Promise((resolve,reject)=>{const x=new XMLHttpRequest();x.open('GET','/data/fixture.json');x.onload=()=>resolve(JSON.parse(x.responseText));x.onerror=reject;x.send()}),new Promise(resolve=>{const e=new EventSource('/api/data-events?session='+encodeURIComponent(PRISM_SESSION));e.onmessage=event=>{e.close();resolve(event.data)}}),new Promise(resolve=>{const w=new WebSocket('ws://'+location.host+'/proxy/fixture/80/ws');w.onopen=()=>w.send('ping');w.onmessage=event=>{w.close();resolve(event.data)}})]).then(results=>{state.results=results;document.getElementById('state').textContent='Ready'}).catch(error=>{state.error=error.message});
      <\/script></body></html>`
      const deliver = (content,restore=true) => page.evaluate(({content,restore})=>fixtureDeliver({type:'plugin_load',id:'fixture-widget',title:'Fixture widget',content,...(restore?{open:true}:{})}),{content,restore})
      const widget = page.frameLocator('.widget-body iframe')
      async function currentWidgetFrame() {
        const element=await page.locator('.widget-body iframe').elementHandle()
        assert(element,'registered widget iframe is present')
        const frame=await element.contentFrame()
        assert(frame,'registered widget document is present')
        return frame
      }
      const paletteKeys = ['--bg1','--text','--text2','--accent','--border','color-scheme']
      async function assertPalette(frame) {
        const expected = await page.evaluate(keys => {
          const vars = PrismTheme.currentVars?.() || PrismTheme.activeVars()
          return Object.fromEntries(keys.map(key => [key,vars[key]]))
        },paletteKeys)
        await frame.waitForFunction(({keys,expected}) => keys.every(key => getComputedStyle(document.documentElement).getPropertyValue(key).trim()===expected[key]),{keys:paletteKeys,expected},{polling:50})
        return expected
      }
      async function assertLoaderPalette(frame) {
        const expected=await assertPalette(frame)
        assert.deepEqual(await frame.evaluate(() => [getComputedStyle(document.documentElement).backgroundColor,getComputedStyle(document.body).backgroundColor]),['rgba(0, 0, 0, 0)','rgba(0, 0, 0, 0)'],'the loading document must not paint a white surface over the themed iframe')
        const rgb = expected['--bg1'].slice(1).match(/../g).map(value=>parseInt(value,16)).join(', ')
        assert.equal(await page.locator('.widget-body iframe').evaluate(el=>getComputedStyle(el).backgroundColor),'rgb('+rgb+')')
      }
      async function assertFirstPaint(name) {
        const expected = await page.evaluate(() => (PrismTheme.currentVars?.() || PrismTheme.activeVars())['--bg1'])
        const shot = await page.locator('.widget-body').screenshot({animations:'disabled'})
        const image = PNG.sync.read(shot)
        const rgb = expected.slice(1).match(/../g).map(value => parseInt(value,16))
        for (const [x,y] of [[8,8],[image.width-9,8],[8,image.height-9],[image.width-9,image.height-9]]) {
          const index = (y*image.width+x)*4
          assert.deepEqual([...image.data.subarray(index,index+3)],rgb,'first paint must use the active palette before widget-frame.js executes ('+name+')')
        }
        if(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR){
          fs.mkdirSync(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR,{recursive:true})
          fs.writeFileSync(path.join(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR,(multi?'multi-':'mono-')+name+'.png'),shot)
        }
      }
      let releaseAuthorize
      authorizeGate=new Promise(resolve=>{releaseAuthorize=resolve})
      let releaseFrameScript
      frameScriptGate=new Promise(resolve=>{releaseFrameScript=resolve})
      try {
        await deliver(content)
        // The HTML may paint while its deferred bootstrap script is still on
        // the network. Check pixels in that interval, before any ready event.
        await widget.locator('#widget-status').waitFor({state:'attached'})
        assert.equal(grants.length,0,'a blocked bootstrap must not request a widget grant')
        await assertFirstPaint('script-pending-initial')
        await page.evaluate(()=>{
          PrismTheme.saveCustomTheme('first-paint-mint','First paint mint',{'--bg':'#b6d3c9','--bg1':'#c6e1d8','--text':'#1c3a31','--text2':'#365f4e','--accent':'#2758aa','--border':'#628c75'})
          PrismTheme.applyTheme('first-paint-mint')
        })
        await assertFirstPaint('script-pending-custom')
        await page.evaluate(()=>PrismTheme.previewVars({...PrismTheme.currentVars(),'--bg':'#d8b99f','--bg1':'#efd4b9','--text':'#3e281c','--text2':'#72533e','--accent':'#7054a3'}))
        await assertFirstPaint('script-pending-preview')
        frameScriptGate=null;releaseFrameScript()
        await widget.locator('#widget-status').waitFor({state:'attached'})
        const loading=await currentWidgetFrame()
        await assertLoaderPalette(loading)
        assert.equal(await page.locator('.widget-body > .widget-loader').isVisible(),true,'the visible loader belongs to the dashboard until the authored document is ready')
        assert.equal(await page.locator('.widget-body iframe').evaluate(el=>getComputedStyle(el).visibility),'hidden')
        assert.equal(await loading.evaluate(()=>document.querySelector('.widget-loader').getAttribute('aria-busy')),'true')
        assert.equal(await loading.evaluate(()=>typeof window.state),'undefined','loading must not execute widget HTML before authorization')
        // A nested/content sender is not the trusted dashboard parent.
        const beforeForged=await loading.evaluate(()=>document.documentElement.style.getPropertyValue('--accent'))
        await loading.evaluate(()=>new Promise(resolve=>{
          const done=event=>{if(event.data?.fixtureForgedTheme){removeEventListener('message',done);resolve()}}
          addEventListener('message',done)
          postMessage({type:'prism-theme',vars:{'--accent':'#ff0000'},fixtureForgedTheme:true},location.origin)
        }))
        assert.equal(await loading.evaluate(()=>document.documentElement.style.getPropertyValue('--accent')),beforeForged,'untrusted theme messages must be ignored')
        if(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR){
          fs.mkdirSync(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR,{recursive:true})
          await page.locator('.widget-window').screenshot({path:path.join(process.env.PRISM_WIDGET_LOADING_SCREENSHOT_DIR,multi?'paper.png':'cyberpunk.png')})
        }
        await page.emulateMedia({reducedMotion:'reduce'})
        await page.evaluate(()=>{
          PrismTheme.saveCustomTheme('loader-fixture','Loading fixture',{'--bg1':'#172737','--text':'#f7f5ed','--text2':'#b9cfdf','--accent':'#73d8bc','--border':'#3b6274'})
          PrismTheme.applyTheme('loader-fixture')
        })
        await assertLoaderPalette(loading)
        assert.equal(await loading.evaluate(()=>document.getAnimations().filter(animation=>animation.playState==='running').length),0,'reduced motion must stop loading animations')
        assert.equal(await page.locator('.widget-body > .widget-loader .widget-loader-beam').evaluate(el=>getComputedStyle(el).animationName),'none','reduced motion must stop the visible parent loading animation')
      } finally {frameScriptGate=null;releaseFrameScript();authorizeGate=null;releaseAuthorize()}
      await widget.getByText('Ready',{exact:true}).waitFor()
      const frame = await currentWidgetFrame()
      await assertPalette(frame)
      const state = await frame.evaluate(()=>state)
      assert.equal(state.parentBlocked,true,'widget must not read the dashboard DOM')
      assert.equal(state.privatePreference,null,'Prism browser preferences must not migrate')
      assert.equal(state.legacyLocal,'ancien choix é');assert.equal(state.legacySession,'current tab é')
      assert.equal(state.results[0].answer,'tool-é');assert.equal(state.results[1].result.answer,'tool-é')
      assert.equal(state.results[2].answer,'data-é');assert.equal(state.results[3].result.answer,'tool-é');assert.equal(state.results[4].answer,'data-é')
      assert.equal(state.results[5],'direct stream');assert.equal(state.results[6],'pong')
      await widget.locator('#pixel').evaluate(img=>img.decode())
      await widget.frameLocator('#service').locator('body[data-working="saved"]').waitFor()
      assert.equal(await widget.locator('html').evaluate(element=>getComputedStyle(element).getPropertyValue('--accent').trim()).then(Boolean),true)
      const download = page.waitForEvent('download');await widget.locator('#export').click();assert.equal((await download).suggestedFilename(),'export.txt')
      await page.evaluate(()=>fixtureDeliver({type:'data_changed',topic:'fixture',reason:'change'}))
      await frame.waitForFunction(()=>state.events===1)

      const beforeRefresh=grants.length
      expired=true
      assert.deepEqual(await frame.evaluate(()=>prismTool('after-refresh')),{answer:'tool-é'})
      assert.equal(grants.length,beforeRefresh+1)
      assert.equal(await frame.evaluate(()=>state.nonce),state.nonce,'authorization refresh must not replace the app')
      assert.equal(await frame.evaluate(()=>localStorage.getItem('fixture-widget-choice')),'nouveau choix é')
      await deliver(content,false)
      await widget.getByText('Ready',{exact:true}).waitFor()
      const reopened=await currentWidgetFrame()
      assert.equal(await reopened.evaluate(()=>state.legacyLocal),'nouveau choix é','native storage must survive widget replacement')

      const beforeRogue=grants.length
      await page.evaluate(origin=>{const frame=document.createElement('iframe');frame.src=origin+'/widget-frame.html?parent='+encodeURIComponent(location.origin);document.body.appendChild(frame)},widgetOrigin)
      await page.waitForTimeout(100)
      assert.equal(grants.length,beforeRogue,'unregistered content frames must not receive grants')

      failGrant=true
      await deliver(content,false)
      await page.locator('.widget-body > .widget-loader .widget-loader-status').filter({hasText:'The widget could not be opened. Reload the page to try again.'}).waitFor()
      const grantFailure=await currentWidgetFrame()
      await assertLoaderPalette(grantFailure)
      assert.equal(await grantFailure.evaluate(()=>typeof window.state),'undefined','failed dashboard grant must not execute authored HTML')
      assert.equal(await page.locator('.widget-body > .widget-loader').getAttribute('aria-busy'),'false')
      failGrant=false
      failAuthorize=true
      await deliver(content,false)
      await page.locator('.widget-body > .widget-loader .widget-loader-status').filter({hasText:'The widget could not be opened. Reload the page to try again.'}).waitFor()
      const accessFailure=await currentWidgetFrame()
      assert.equal(await accessFailure.evaluate(()=>typeof window.state),'undefined','failed authorization must not execute authored HTML')
      await assertLoaderPalette(accessFailure)
      assert.equal(await page.locator('.widget-body > .widget-loader').getAttribute('aria-busy'),'false')
      identityUnavailable=true
      await page.reload()
      await page.waitForFunction(()=>fixtureSockets.length>0)
      await deliver(content)
      await page.locator('.widget-body').getByText('The widget could not be opened. Reload the page to try again.').waitFor()
      assert.equal(await page.locator('.widget-body iframe').count(),0,'identity failure must never fall back to same-origin srcdoc')
      assert.deepEqual(errors,[])
      assert(authorizations>=4);assert(tools.includes('/api/tool/legacy'))
      await context.close()
    }
    console.log('Widget origins: themed light/dark/custom loading and errors, reduced motion, mono/multi DOM isolation, grants, transparent tools/data/XHR/SSE/WebSocket/assets/downloads/storage, nested apps, signals, refresh without rerender and fail-closed loading passed')
  } finally {await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
