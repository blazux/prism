const {chromium} = require('playwright')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const root = path.resolve(__dirname, '..'), origin = 'http://localhost', widgetOrigin = 'http://localhost:48082'
const wait = async (predicate, label) => {
  const until = Date.now() + 7000
  while (!predicate()) {if (Date.now() > until) throw Error('Timed out: ' + label); await new Promise(resolve => setTimeout(resolve, 20))}
}
;(async () => {
  const browser = await chromium.launch({headless:true, ...(process.env.CHROMIUM_EXECUTABLE_PATH ? {executablePath:process.env.CHROMIUM_EXECUTABLE_PATH} : {})})
  try {
    for (const multi of [false, true]) {
      const context = await browser.newContext({viewport:{width:1200,height:850}}), page = await context.newPage()
      page.setDefaultTimeout(8000)
      const errors = [], reads = [], writes = [], navigations = []
      let failNoteSave=false, holdNoteSave=false, releaseNoteSave
      let failDraftSave=false, holdDraftSave=false, releaseDraftSave
      const session = multi ? 'u7-main' : 'main', assistant = multi ? 'u7-assistant' : 'assistant'
      const noteID = 'Folder/A & B #é.md', secondNoteID = 'Folder/Other.md'
      const notes=new Map([noteID,secondNoteID].map(id=>[id,{id,title:id===noteID?'Linked note':'Second note',body:'Saved body',tags:'',scope:'personal',canEdit:true}]))
      const listedNotes=new Set(), drafts=new Map()
      page.on('pageerror', error => errors.push(error.message))
      await page.addInitScript(() => {
        window.fixtureSockets = []
        window.WebSocket = class {
          static OPEN = 1; static CLOSED = 3
          constructor(url) {this.url=url;this.readyState=1;this.messages=[];fixtureSockets.push(this);queueMicrotask(()=>this.onopen?.())}
          send(value) {this.messages.push(JSON.parse(value))}
          close() {this.readyState=3;this.onclose?.()}
        }
        window.deliverFixture = message => fixtureSockets.at(-1).onmessage({data:JSON.stringify(message)})
        Object.defineProperty(navigator,'clipboard',{value:{writeText:async text=>{window.fixtureCopied=text}}})
      })
      await context.route('**/*', async route => {
        const request=route.request(), url=new URL(request.url())
        if (url.origin === widgetOrigin) {
          if (url.pathname === '/__prism_widget/authorize') return route.fulfill({contentType:'application/json',body:'{"ok":true}'})
          const file=path.resolve(root,'.'+url.pathname)
          return file.startsWith(root+path.sep)&&fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''})
        }
        if (url.origin !== origin) {
          if (url.href === 'https://cdn.jsdelivr.net/npm/marked@9/marked.min.js') return route.fulfill({path:path.join(root,'vendor/marked/marked.min.js')})
          return route.abort()
        }
        if (url.pathname === '/app.js') return route.fulfill({contentType:'text/javascript',body:fs.readFileSync(path.join(root,'app.js'),'utf8')+'\nwindow.fixtureLinks={get view(){return currentView},get session(){return currentSessionID},get ready(){return !!activeEditorSource},chat(){if(!chatOpen)toggleChat()}}; '})
        if (!url.pathname.startsWith('/api/')) {
          const file=path.resolve(root,url.pathname==='/'?'index.html':'.'+url.pathname)
          if (url.pathname.startsWith('/apps/')) navigations.push(url.pathname+url.search)
          return file.startsWith(root+path.sep)&&fs.existsSync(file)&&fs.statSync(file).isFile()?route.fulfill({path:file}):route.fulfill({status:404,body:''})
        }
        const mutation=!['GET','HEAD'].includes(request.method())
        if (mutation) writes.push({path:url.pathname,query:url.search,body:request.postData()})
        else reads.push(url.pathname+url.search)
        let data={},status=200
        if (url.pathname==='/api/auth') data={authenticated:true,auth_enabled:false}
        else if(url.pathname==='/api/me') data={authenticated:true,multiUser:multi,isAdmin:!multi,user:multi?{id:7,role:'member'}:undefined,widgetHosting:{frameURL:widgetOrigin+'/widget-frame.html?parent='+encodeURIComponent(origin),grantURL:'/api/widget/grant'}}
        else if(url.pathname==='/api/widget/grant') data={token:'fixture-widget-grant'}
        else if(url.pathname==='/api/profile') data={userId:multi?7:0,displayName:'Fixture'}
        else if(url.pathname==='/api/platform') data={disabledApps:[]}
        else if(url.pathname==='/api/my/groups') data={groups:[]}
        else if(url.pathname==='/api/models') data={models:['fixture']}
        else if(url.pathname==='/api/agent/name') data={name:'Fixture'}
        else if(url.pathname==='/api/vox-workspace') data={available:false}
        else if(url.pathname==='/api/resources') data=[]
        else if(url.pathname==='/api/builtin/email') data={result:[{name:'INBOX',role:'inbox',selectable:true},{name:'Archive/2024',selectable:true}]}
        else if(url.pathname==='/api/sessions') data={sessions:[{id:session,name:'Main'},...(url.searchParams.has('includeSystem')?[{id:multi?'u7-telegram':'telegram',name:'Telegram'}]:[])]}
        else if(url.pathname==='/api/notes') {
          if(mutation) {
            // A slow blur-save must not drop a linked note fetched outside
            // the list when its post-save refresh replaces that list.
            if(JSON.parse(request.postData()).body==='Keep this draft through the link') await new Promise(resolve=>setTimeout(resolve,150))
            if(holdNoteSave){holdNoteSave=false;await new Promise(resolve=>releaseNoteSave=resolve)}
            if(failNoteSave){status=503;data={error:'Fixture note save unavailable'}}
            else {
              const incoming=request.postDataJSON()
              notes.set(incoming.id,{...notes.get(incoming.id),...incoming})
              listedNotes.add(incoming.id)
              data={id:incoming.id}
            }
          }
          else if(url.searchParams.has('id')) {
            const id=url.searchParams.get('id')
            data={note:notes.get(id),source:'vault'}
          } else data={notes:[...listedNotes].map(id=>notes.get(id)),source:'vault'}
        } else if(url.pathname==='/api/tasks') {
          const id=url.searchParams.get('id')
          if(id==='missing'){status=404;data={error:'Task not found or unavailable.'}}
          else if(id) data={task:{id,title:'Completed linked task',priority:'normal',done:true},source:'local'}
          else data={tasks:[],source:'local'}
        } else if(url.pathname==='/api/events') {
          const id=url.searchParams.get('id')
          data=id?{event:{id,title:'Far future event',startAt:'2099-06-15T10:00:00Z',endAt:'2099-06-15T11:00:00Z'},source:'local'}:{events:[],source:'local'}
        } else if(url.pathname==='/api/email/config') data={configured:true,has_password:true,user:'fixture@example.test',list_limit:2}
        else if(url.pathname==='/api/email/folders') data={folders:['INBOX','Archive/2024']}
        else if(url.pathname==='/api/email/list') data={messages:[]}
        else if(url.pathname==='/api/email/drafts') {
          if(request.method()==='POST') {
            if(holdDraftSave){holdDraftSave=false;await new Promise(resolve=>releaseDraftSave=resolve)}
            if(failDraftSave){status=503;data={error:'Fixture draft save unavailable'}}
            else {
              const incoming=request.postDataJSON(),id=incoming.id||drafts.size+1,previous=drafts.get(id)
              if(previous&&previous.revision!==incoming.revision){status=409;data={error:'Draft changed'}}
              else {
                const draft={...previous,...incoming,id,revision:(previous?.revision||0)+1,status:'draft',updatedAt:'2026-11-24T16:00:00Z'}
                drafts.set(id,draft);data={draft}
              }
            }
          } else if(url.searchParams.has('id'))data={draft:drafts.get(Number(url.searchParams.get('id'))),mailboxChanged:false}
          else data={drafts:[...drafts.values()]}
        }
        else if(url.pathname==='/api/email/read') data={message:{uid:Number(url.searchParams.get('uid')),subject:'Archived linked message',from:'friend@example.test',to:'fixture@example.test',body:'Older than the loaded page',seen:true,date:'2024-01-01T10:00:00Z'}}
        return route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)})
      })
      let linkNumber=0
      const chatLink=async(href,label='Open linked item '+(++linkNumber))=>{
        await page.evaluate(({href,label})=>{
          fixtureLinks.chat()
          deliverFixture({type:'chat_history',messages:[{role:'assistant',content:'['+label+']('+href+')'}]})
        },{href,label})
        await page.locator('#chat-messages').getByRole('link',{name:label,exact:true}).click()
      }
      await page.goto(origin+'/?app=notes&id='+encodeURIComponent(noteID)+'&workspace=unrelated')
      const app=page.frameLocator('#app-frame')
      await app.locator('#e-title').waitFor()
      await page.waitForFunction(()=>fixtureLinks.ready)
      assert.equal(await app.locator('#e-title').inputValue(),'Linked note')
      await app.locator('.note-actions > summary').click()
      await app.getByRole('button',{name:'Copy link',exact:true}).click()
      await app.locator('.note-actions > summary').click()
      assert.equal(await page.frames().find(f=>f.url().includes('/apps/notes.html')).evaluate(()=>fixtureCopied),origin+'/?app=notes&id='+encodeURIComponent(noteID).replaceAll('%20','+'))
      assert.equal(await page.evaluate(()=>fixtureLinks.session),assistant)
      const notesNavigation=navigations.length
      await app.locator('#e-body').fill('Keep this draft through the link')
      await chatLink('/?app=notes&id='+encodeURIComponent(secondNoteID))
      await page.frames().find(frame=>frame.url().includes('/apps/notes.html')).waitForFunction(()=>document.getElementById('e-title').value==='Second note',null,{timeout:8000})
      assert.equal(navigations.length,notesNavigation,'same-app links preserve the iframe')
      assert(writes.some(w=>w.path==='/api/notes'&&JSON.parse(w.body).body==='Keep this draft through the link'),'notes retain existing autosave protection')

      await app.locator('#e-body').fill('Cannot save this yet')
      failNoteSave=true
      await chatLink('/?app=tasks&id=blocked-by-save-error')
      await app.getByText('Not saved: Fixture note save unavailable',{exact:true}).waitFor()
      assert.equal(navigations.length,notesNavigation)
      assert.equal(await app.locator('#e-body').inputValue(),'Cannot save this yet')
      failNoteSave=false;holdNoteSave=true
      await chatLink('/?app=tasks&id='+encodeURIComponent('task/older & done'))
      await wait(()=>releaseNoteSave,'pending note save before navigation')
      await app.locator('#e-body').fill('Newer edit typed while saving')
      releaseNoteSave()
      await app.locator('dialog[open] input[name=title]').fill('My unsaved task')
      assert(writes.some(w=>w.path==='/api/notes'&&JSON.parse(w.body).body==='Newer edit typed while saving'),'newer typing is saved before leaving Notes')
      const tasksNavigation=navigations.length
      await page.waitForFunction(()=>fixtureLinks.ready)
      await chatLink('/?app=tasks&id=another')
      await app.getByText(/Close the current task form/).waitFor()
      assert.equal(await app.locator('dialog[open] input[name=title]').inputValue(),'My unsaved task')
      assert.equal(navigations.length,tasksNavigation)
      await chatLink('/?app=calendar&id=would-lose-task')
      await app.getByText('Discard unsaved changes before opening this link?',{exact:true}).waitFor()
      await app.locator('.pm-overlay').getByRole('button',{name:'Cancel',exact:true}).click()
      assert.equal(await app.locator('dialog[open] input[name=title]').inputValue(),'My unsaved task')
      assert.equal(navigations.length,tasksNavigation,'canceling cross-app navigation keeps the editor')
      await app.locator('dialog[open]').getByRole('button',{name:'Cancel',exact:true}).click()
      await chatLink('/?app=tasks&id=missing')
      await app.getByText(/Task not found or unavailable/).waitFor()
      assert.equal(await app.locator('dialog[open]').count(),0)

      await chatLink('/?app=calendar&id='+encodeURIComponent('calendar/future event'))
      await app.locator('#overlay.open').waitFor()
      assert.equal(await app.locator('#f-date').inputValue(),'2099-06-15')
      assert.equal(await app.locator('#f-title').inputValue(),'Far future event')
      assert(reads.some(p=>p.startsWith('/api/events?id=calendar%2Ffuture%20event')))

      await chatLink('/?app=email&id=153&folder=Archive%2F2024')
      await app.locator('#reading').getByText('Archived linked message',{exact:true}).waitFor()
      assert(reads.some(p=>p.startsWith('/api/email/read?')&&p.includes('folder=Archive%2F2024')&&p.includes('uid=153')))
      await app.locator('.new-message').click()
      await app.locator('#c-body').fill('Keep my mail draft')
      await page.waitForFunction(()=>fixtureLinks.ready)
      await chatLink('/?app=email&id=154')
      await app.getByText(/Close the current draft/).waitFor()
      assert.equal(await app.locator('#c-body').inputValue(),'Keep my mail draft')

      failDraftSave=true
      await app.locator('#c-body').fill('Keep my mail draft after an edit')
      await chatLink('/?workspace='+session)
      await app.getByText('Draft not saved: Fixture draft save unavailable',{exact:true}).waitFor()
      assert.equal(await app.locator('#c-body').inputValue(),'Keep my mail draft after an edit')
      assert.equal(await page.evaluate(()=>fixtureLinks.view.type),'app','failed draft save blocks navigation')
      failDraftSave=false;holdDraftSave=true
      await chatLink('/?workspace='+session)
      await wait(()=>releaseDraftSave,'mail draft save before navigation')
      assert.equal(await page.evaluate(()=>fixtureLinks.view.type),'app','navigation waits for durable draft save')
      releaseDraftSave()
      await page.waitForFunction(id=>fixtureLinks.view.type==='board'&&fixtureLinks.session===id,session)
      assert([...drafts.values()].some(draft=>draft.body==='Keep my mail draft after an edit'),'mail draft survives leaving the app')
      await page.evaluate(()=>deliverFixture({type:'plugin_load',id:'links',title:'Links widget',content:'<a href="/?app=tasks&id=from-widget">Task from widget</a>',open:true}))
      await page.frameLocator('.widget-body iframe').getByRole('link',{name:'Task from widget'}).click()
      await app.locator('dialog[open] input[name=title]').waitFor()
      assert(reads.includes('/api/tasks?id=from-widget'))
      assert.equal(context.pages().length,1,'chat and widget links open in the same shell')
      assert.deepEqual(errors,[])
      await context.close()
    }
    console.log('Object links passed: mono/multi startup, chat and widget navigation, encoded IDs, older email, completed task, future event, same-app draft protection and missing item errors.')
  } finally {await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
