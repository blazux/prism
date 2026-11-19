// Ordinary Prism URLs address an app object without carrying user credentials
// or selecting another account. The app's API resolves its current owner/source.
;(function () {
  const apps = new Set(['email', 'notes', 'tasks', 'calendar'])
  const validID = value => typeof value === 'string' && value.trim() && value.length <= 2048 && !/[\x00-\x1f\x7f]/.test(value)
  function parse(value, base = location.origin) {
    let url
    try { url = new URL(value, base + '/') } catch (_) { return null }
    if (url.origin !== base || url.pathname !== '/' || url.username || url.password) return null
    const q = url.searchParams
    if (q.has('view')) return null
    for (const name of ['app', 'id', 'folder', 'workspace']) if (q.getAll(name).length > 1) return null
    const app = q.get('app')
    if (apps.has(app)) {
      const id = q.get('id'), folder = q.get('folder')
      if (id !== null && !validID(id)) return null
      if (app === 'email' && id !== null && (!/^[1-9]\d{0,9}$/.test(id) || Number(id) > 4294967295)) return null
      if (folder !== null && (app !== 'email' || !validID(folder))) return null
      return {type: 'app', name: app, ...(id === null ? {} : {id}), ...(folder === null ? {} : {folder})}
    }
    const workspace = q.get('workspace')
    return !app && validID(workspace) ? {type: 'board', workspace, ...(q.get('chat') === 'open' ? {chat:'open'} : {})} : null
  }
  function href(view) {
    const q = new URLSearchParams()
    if (view.type === 'board') {
      q.set('workspace', view.workspace)
      if (view.chat === 'open') q.set('chat', 'open')
    }
    else {
      q.set('app', view.name)
      if (view.id !== undefined) q.set('id', view.id)
      if (view.folder !== undefined) q.set('folder', view.folder)
    }
    return '/?' + q
  }
  function bind(open, doc = document) {
    const click = event => {
      if (event.defaultPrevented || event.button > 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
      const anchor = event.target.closest?.('a[href]')
      if (!anchor || anchor.hasAttribute('download')) return
      const view = parse(anchor.getAttribute('href'))
      if (!view) return
      event.preventDefault()
      open(view)
    }
    doc.addEventListener('click', click)
    return () => doc.removeEventListener('click', click)
  }
  function current(app) {
    const q = new URLSearchParams(location.search)
    q.set('app', app)
    return parse('/?' + q)
  }
  function receive(app, open) {
    let ready = false, pending = current(app), generation = 0
    const deliver = view => {
      const request = ++generation
      Promise.resolve().then(() => open(view, () => request === generation)).catch(error => {
        if (request === generation) window.PrismAPI?.toast('Could not open this link: ' + error.message)
      })
    }
    addEventListener('message', event => {
      if (event.source !== parent || event.origin !== location.origin || event.data?.type !== 'open-reference-object') return
      const view = parse(event.data.href)
      if (view?.type !== 'app' || view.name !== app) return
      if (ready) deliver(view)
      else pending = view
    })
    return () => {
      ready = true
      if (pending) { const view = pending; pending = null; deliver(view) }
    }
  }
  async function copy(view) {
    const url = new URL(href(view), location.origin).href
    try {
      await navigator.clipboard.writeText(url)
      window.PrismAPI?.toast('Link copied.', 'ok')
    } catch (_) {
      await window.PrismModal?.prompt('Copy link', [{name:'link',label:'Link',value:url}], {okLabel:'Done'})
    }
  }
  async function confirmLeave(read, saved) {
    const draft = read()
    if (draft === saved) return true
    if (!await window.PrismModal?.confirm('Discard unsaved changes before opening this link?', {danger:true,okLabel:'Discard'})) return false
    // Typing while the confirmation is open must not authorize newer edits.
    return read() === draft
  }
  window.PrismLinks = {parse, href, bind, current, receive, copy, confirmLeave}
  // An app embedded in the shell opens references in that shell. Standalone
  // app pages keep normal browser navigation as their fallback.
  if (window.parent !== window) bind(view => parent.postMessage({type: 'open-reference', href: href(view)}, location.origin))
})()
