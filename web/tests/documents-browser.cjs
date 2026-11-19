const {chromium} = require('playwright')
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path')
const root = path.resolve(__dirname, '..'), origin = 'http://documents.invalid'
const wait = async (predicate, label) => {
  const until = Date.now() + 7000
  while (!predicate()) { if (Date.now() > until) throw Error('Timed out: ' + label); await new Promise(resolve => setTimeout(resolve, 20)) }
}
;(async () => {
  const executablePath = process.env.CHROMIUM_EXECUTABLE_PATH || process.env.CHROMIUM_EXECUTABLE
  const browser = await chromium.launch({headless:true, ...(executablePath ? {executablePath} : {})})
  try {
    for (const host of ['settings.html', 'rag.html']) {
      const context = await browser.newContext({viewport:{width:1280,height:850}}), page = await context.newPage()
      page.setDefaultTimeout(8000)
      const errors = [], reads = [], writes = [], uploads = []
      let visit = 0, role = 'mono', failCreate = false, holdA = false, releaseA, releaseUpload, deleted = false
      const collections = [{name:'A',description:'Manuals',doc_count:1},{name:'B',description:'Recipes',doc_count:1}]
      page.on('pageerror', error => errors.push(error.message))
      await context.route('**/*', async route => {
        const request = route.request(), url = new URL(request.url())
        if (url.origin !== origin) return route.abort()
        if (!url.pathname.startsWith('/api/')) {
          const file = path.join(root, url.pathname)
          return fs.existsSync(file) && fs.statSync(file).isFile() ? route.fulfill({path:file}) : route.fulfill({status:404,body:''})
        }
        const mutation = !['GET','HEAD'].includes(request.method())
        if (!mutation) reads.push(url.pathname + url.search)
        else writes.push({path:url.pathname,query:url.search,body:request.postData()})
        let data = {}, status = 200
        if (url.pathname === '/api/me') data = {authenticated:true,multiUser:role !== 'mono',isAdmin:role === 'mono',...(role !== 'mono' ? {user:{id:1,role:'member'}} : {})}
        else if (url.pathname === '/api/my/groups') data = {groups:role === 'group' ? [{id:2,name:'Team'}] : []}
        else if (url.pathname === '/api/platform') data = {disabledApps:[],voxDocked:false}
        else if (url.pathname === '/api/auth') data = {authenticated:true,auth_enabled:false}
        else if (url.pathname === '/api/memory') data = {profile:[],learnings:[]}
        else if (url.pathname === '/api/rag/status') data = {ready:true}
        else if (url.pathname === '/api/rag/collections') {
          if (request.method() === 'PATCH') {
            if (failCreate) {status = 503; data = {error:'fixture create failed'}}
            else {
              const body = request.postDataJSON(), collection = collections.find(c => c.name === body.name)
              if (collection) collection.description = body.description
              else collections.push({...body,doc_count:0})
            }
          } else data = collections
        } else if (url.pathname === '/api/rag/documents') {
          const collection = url.searchParams.get('collection')
          if (collection === 'A' && holdA) await new Promise(resolve => {releaseA = resolve})
          data = collection === 'A' && deleted ? [] : [{id:collection === 'A' ? 1 : 2,filename:collection + ' document',size_bytes:100,chunk_count:1,updated_at:'2026-11-18'}]
        } else if (url.pathname === '/api/rag/document') {deleted = true; data = {ok:true}}
        else if (url.pathname === '/api/rag/upload') {
          uploads.push(request.postData())
          if (uploads.length === 1) await new Promise(resolve => {releaseUpload = resolve})
          data = {chunks:1}
        } else if (url.pathname === '/api/rag/upload/progress') data = {found:true,stage:'embedding',total:3,done:1,rate:2,etaSecs:1}
        return route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)})
      })
      const open = async () => {
        await page.goto(origin + '/' + host + '?session=unrelated&visit=' + (++visit) + '#knowledge')
        if (host === 'settings.html') await page.getByRole('button',{name:'Documents',exact:true}).click()
        await page.locator('#rag-tab').waitFor()
      }
      await open()
      await page.locator('.collection-item').filter({hasText:'A'}).first().click()
      await page.locator('#docs-table').getByText('A document',{exact:true}).waitFor()
      holdA = true
      await page.locator('.collection-item').filter({hasText:'B'}).first().click()
      await page.locator('#docs-table').getByText('B document',{exact:true}).waitFor()
      await page.locator('.collection-item').filter({hasText:'A'}).first().click()
      await wait(() => releaseA, host + ' pending A')
      await page.locator('.collection-item').filter({hasText:'B'}).first().click()
      await page.locator('#docs-table').getByText('B document',{exact:true}).waitFor()
      releaseA(); holdA = false
      await page.waitForTimeout(100)
      assert.match(await page.locator('#docs-area').textContent(), /B document/)
      assert.doesNotMatch(await page.locator('#docs-area').textContent(), /A document/)

      await page.locator('#new-collection-btn').click()
      await page.locator('#modal-input').fill('New Manual')
      await page.locator('#modal-desc').fill('Equipment service instructions')
      failCreate = true
      await page.locator('#collection-create').click()
      await page.getByText('Could not create collection: fixture create failed',{exact:true}).waitFor()
      assert.equal(await page.locator('#modal-input').inputValue(),'New Manual')
      assert.equal(await page.locator('#modal-desc').inputValue(),'Equipment service instructions')
      failCreate = false
      await page.locator('#collection-create').click()
      await page.locator('#modal-overlay').waitFor({state:'hidden'})
      await page.locator('.collection-name').getByText('new-manual',{exact:true}).waitFor()
      assert(writes.some(w => w.path === '/api/rag/collections' && JSON.parse(w.body).description === 'Equipment service instructions'))

      await page.locator('.collection-item').filter({hasText:'A'}).first().click()
      await page.locator('#upload-input').setInputFiles([{name:'first.txt',mimeType:'text/plain',buffer:Buffer.from('one')},{name:'second.txt',mimeType:'text/plain',buffer:Buffer.from('two')}])
      await wait(() => releaseUpload, host + ' upload')
      await wait(() => reads.some(p => p.startsWith('/api/rag/upload/progress?collection=A&filename=first.txt')), host + ' upload progress')
      await page.locator('.collection-item').filter({hasText:'B'}).first().click()
      releaseUpload()
      await wait(() => uploads.length === 2, host + ' second upload')
      assert(uploads.every(body => /name="collection"\r\n\r\nA\r\n/.test(body)), host + ' keeps the upload batch in A')
      await page.locator('#docs-table').getByText('B document',{exact:true}).waitFor()
      assert(reads.filter(p => p.startsWith('/api/rag/')).every(p => !p.includes('session=')), host + ' no workspace in Documents API requests')

      await page.setViewportSize({width:390,height:844})
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth),false,host + ' mobile width')
      await page.screenshot({path:'/tmp/prism-documents-' + host.replace('.html','') + '-mobile.png',fullPage:true})
      for (const state of ['group','groupless']) {
        role = state
        const writesBefore = writes.length, collectionsBefore = reads.filter(p => p.startsWith('/api/rag/collections')).length
        await open()
        assert.equal(await page.locator('#new-collection-btn').count(),0,host + ' ' + state + ' does not offer creation')
        if (state === 'group') {
          await page.locator('.collection-item').first().click()
          await page.locator('#docs-table').waitFor()
          assert.equal(await page.locator('#desc-input,#upload-zone,.collection-del,.doc-del-btn').count(),0,host + ' read-only group')
        } else {
          assert.equal(reads.filter(p => p.startsWith('/api/rag/collections')).length,collectionsBefore,host + ' groupless users do not read group collections')
          assert.match(await page.locator('#rag-sidebar').textContent(),/not part of a group/)
        }
        assert.equal(writes.length,writesBefore)
      }
      assert.deepEqual(errors,[],host + ' page errors')
      await context.close()
    }
    console.log('Shared Documents passed on Settings and legacy page: stale reads, create failure/retry, upload target/progress, mono/group/groupless rights, workspace-independent requests and mobile layout.')
  } finally {await browser.close()}
})().catch(error => {console.error(error);process.exitCode=1})
