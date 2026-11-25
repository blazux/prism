const {chromium} = require('playwright')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path'), os = require('node:os'), childProcess = require('node:child_process')
const repo = path.resolve(__dirname,'../..')
const delay = ms => new Promise(resolve=>setTimeout(resolve,ms))

;(async()=>{
  const temporary=fs.mkdtempSync(path.join(os.tmpdir(),'prism-widget-backend-')), ready=path.join(temporary,'ready.json')
  const binary=process.env.PRISM_WIDGET_FIXTURE_BINARY
  const fixture=childProcess.spawn(binary||'go',binary?['-test.run=^TestWidgetBrowserFixture$','-test.v','-test.timeout=150s']:['test','-count=1','-run','^TestWidgetBrowserFixture$','./internal/server','-v','-timeout','150s'],{
    cwd:repo,env:{...process.env,PRISM_WIDGET_BROWSER_READY:ready},stdio:['ignore','pipe','pipe']
  })
  let output='',fixtureError,base,browser,context,completed=false
  fixture.stdout.on('data',value=>output+=value);fixture.stderr.on('data',value=>output+=value)
  fixture.on('error',error=>fixtureError=error)
  try{
    const until=Date.now()+90000
    while(!fs.existsSync(ready)){
      if(fixtureError)throw fixtureError
      if(fixture.exitCode!==null)throw Error('Widget fixture exited: '+output.slice(-12000))
      if(Date.now()>until)throw Error('Widget fixture did not start: '+output.slice(-12000))
      await delay(50)
    }
    base=JSON.parse(fs.readFileSync(ready,'utf8'))
    browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.CHROMIUM_EXECUTABLE_PATH}:{})})
    context=await browser.newContext({viewport:{width:1200,height:850}})
    const page=await context.newPage(),errors=[]
    page.setDefaultTimeout(10000)
    page.on('pageerror',error=>errors.push(error.message))
    await context.route('**/*',route=>{
      const url=new URL(route.request().url())
      assert([base.main,base.widget].includes(url.origin),'fixture must not contact external services: '+url.href)
      return route.continue()
    })
    await page.goto(base.main+'/__widget_fixture_host')
    const document=`<!doctype html><html><head></head><body><p id="status">Opening</p><script>
      window.state={};try{parent.document.getElementById('private-marker');state.parentBlocked=false}catch(_){state.parentBlocked=true}
      localStorage.setItem('fixture-native-storage','saved é');state.storage=localStorage.getItem('fixture-native-storage');
      Promise.all([prismTool('widget_fixture'),prismToolRaw('widget_fixture'),fetch('/data/fixture.json').then(r=>r.json()),fetch('/api/admin/platform').then(r=>r.status),fetch(${JSON.stringify(base.main+'/api/admin/platform')},{credentials:'include'}).then(()=>false,()=>true),new Promise((resolve,reject)=>{const socket=new WebSocket('ws://'+location.host+'/proxy/3000/ws');socket.onopen=()=>socket.send('ping');socket.onmessage=e=>{socket.close();resolve(e.data)};socket.onerror=()=>reject(new Error('Service WebSocket failed'))})]).then(results=>{state.results=results;document.getElementById('status').textContent='Ready'}).catch(error=>{state.error=error.message;document.getElementById('status').textContent='Error: '+error.message});
    <\/script></body></html>`
    await page.evaluate(html=>showFixture(html),document)
    const widget=page.frameLocator('main > iframe')
    await widget.getByText('Ready',{exact:true}).waitFor().catch(async error=>{
      for(const frame of page.frames())console.error(JSON.stringify({url:frame.url(),body:await frame.locator('body').innerText().catch(()=>''),state:await frame.evaluate(()=>window.state).catch(()=>null)}))
      throw error
    })
    const frame=page.frames().find(frame=>frame.url().startsWith(base.widget+'/widget-frame.html'))
    const state=await frame.evaluate(()=>state)
    assert.equal(state.parentBlocked,true)
    assert.equal(state.storage,'saved é')
    assert.deepEqual(state.results.slice(0,3),[{answer:'tool-é'},{result:{answer:'tool-é'},images:[]},{answer:'data-é'}])
    assert.equal(state.results[3],403,'content-origin control API must stay blocked')
    assert.equal(state.results[4],true,'the content document must not read the main API')
    assert.equal(state.results[5],'pong','proxied WebSocket must retain native operation')
    assert.equal(await frame.evaluate(()=>document.cookie.includes('prism_session=')||document.cookie.includes('prism_widget=')),false,'content scripts must not read either HTTP-only credential')

    // Navigate the generated document to each main-origin active-content path.
    // A same-origin regain would expose the dashboard DOM after that navigation.
    for(const activePath of ['/plugins/main/fixture.html','/data/fixture.html','/proxy/3000/page']){
      const current=page.frames().find(frame=>frame.parentFrame()===page.mainFrame())
      await current.evaluate(destination=>{location.href=destination},base.main+activePath)
      await widget.locator('body[data-ready="true"]').waitFor()
      const navigated=page.frames().find(frame=>frame.parentFrame()===page.mainFrame())
      assert.equal(new URL(navigated.url()).origin,base.widget,'generated content must remain on the content origin')
      assert.equal(new URL(navigated.url()).searchParams.has('widget_grant'),false,'one-time URL credential must be scrubbed')
      assert.equal(await widget.locator('body').getAttribute('data-parent-blocked'),'true')
    }
    const service=page.frames().find(frame=>frame.parentFrame()===page.mainFrame())
    assert.deepEqual(await service.evaluate(()=>fetch('/proxy/3000/state').then(r=>r.json())),{answer:'service-é'})
    const stats=await page.request.get(base.main+'/__widget_fixture_state').then(r=>r.json())
    assert(stats.tools>=2)
    assert.equal(stats.proxyCredential,false,'actual proxy must not forward Prism login or widget credentials')
    assert.equal(stats.serviceCookie,true,'actual proxy must preserve the service own cookie')

    await context.clearCookies({name:'prism_widget'})
    assert.equal((await page.request.get(base.widget+'/data/fixture.json')).status(),401,'main cookie alone must not authorize the content origin')
    assert.equal((await page.request.get(base.main+'/api/auth').then(r=>r.json())).authenticated,true,'dashboard cookie remains valid on the main origin')
    assert.deepEqual(errors,[])
    completed=true
    console.log('Actual widget backend: main/content HTTP listeners, opaque grants, tools/data, native storage/WebSocket, active-document redirects, parent/API isolation and service cookie boundaries passed')
  }finally{
    if(context)await context.close()
    if(browser)await browser.close()
    fs.writeFileSync(ready+'.stop','')
    if(base)await fetch(base.main+'/__widget_fixture_stop',{method:'POST'}).catch(()=>{})
    for(let count=0;fixture.exitCode===null&&count<100;count++)await delay(50)
    if(fixture.exitCode===null)fixture.kill('SIGKILL')
    if(fixture.exitCode!==0&&base)process.stderr.write(output.slice(-12000))
    fs.rmSync(temporary,{recursive:true,force:true})
    if(completed&&fixture.exitCode!==0)throw Error('Widget backend fixture did not finish successfully')
  }
})().catch(error=>{console.error(error);process.exitCode=1})
