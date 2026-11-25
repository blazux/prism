const {chromium} = require('playwright')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const root = path.resolve(__dirname, '..')
const mainOrigin = 'http://127.0.0.1:48080', widgetOrigin = 'http://127.0.0.1:48082', serviceOrigin = 'http://service.invalid'
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=','base64')

;(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.CHROMIUM_EXECUTABLE_PATH ? {executablePath:process.env.CHROMIUM_EXECUTABLE_PATH} : {})})
  try {
    for (const multi of [false, true]) {
      const context = await browser.newContext({viewport:{width:1200,height:850}}), page = await context.newPage()
      const session = multi ? 'u7-main' : 'main', errors = [], tools = [], grants = []
      let authorizations = 0, expired = false, identityUnavailable = false, failAuthorize = false
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
      await context.route('**/*', async route => {
        const request = route.request(), url = new URL(request.url())
        if (url.origin === serviceOrigin) return route.fulfill({contentType:'text/html',body:'<!doctype html><body>Nested app<script>localStorage.setItem("nested-app","saved");document.body.dataset.working=localStorage.getItem("nested-app")</script>'})
        assert([mainOrigin,widgetOrigin].includes(url.origin),'unexpected external request: '+url.href)
        if (url.origin === widgetOrigin) {
          if (url.pathname === '/__prism_widget/authorize') {
            authorizations++
            assert.match(request.postDataJSON().token,/^fixture-grant-/)
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
      const deliver = html => page.evaluate(content=>fixtureDeliver({type:'plugin_load',id:'fixture-widget',title:'Fixture widget',content,open:true}),html)
      await deliver(content)
      const widget = page.frameLocator('.widget-body iframe')
      await widget.getByText('Ready',{exact:true}).waitFor()
      const frame = page.frames().find(frame=>frame.url().startsWith(widgetOrigin+'/widget-frame.html'))
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
      await deliver(content)
      await widget.getByText('Ready',{exact:true}).waitFor()
      const reopened=page.frames().find(frame=>frame.url().startsWith(widgetOrigin+'/widget-frame.html'))
      assert.equal(await reopened.evaluate(()=>state.legacyLocal),'nouveau choix é','native storage must survive widget replacement')

      const beforeRogue=grants.length
      await page.evaluate(origin=>{const frame=document.createElement('iframe');frame.src=origin+'/widget-frame.html?parent='+encodeURIComponent(location.origin);document.body.appendChild(frame)},widgetOrigin)
      await page.waitForTimeout(100)
      assert.equal(grants.length,beforeRogue,'unregistered content frames must not receive grants')

      failAuthorize=true
      await deliver(content)
      await widget.getByText('The widget could not be opened. Reload the page to try again.').waitFor()
      assert.equal(await page.frames().find(frame=>frame.url().startsWith(widgetOrigin+'/widget-frame.html')).evaluate(()=>typeof window.state),'undefined','failed authorization must not execute authored HTML')
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
    console.log('Widget origins: mono/multi DOM isolation, grants, transparent tools/data/XHR/SSE/WebSocket/assets/downloads/storage, nested apps, signals, refresh without rerender and fail-closed loading passed')
  } finally {await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
