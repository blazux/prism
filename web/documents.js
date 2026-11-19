// Documents has one implementation for Settings and the legacy /rag page.
// Request authorization and identity/group scope remain enforced by the server.
window.PrismDocuments = (() => {
  function escHtml(value) {
    return String(value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
  }
  function defaultIcon(name, size = 18) {
    const paths = {
      knowledge: '<path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2zM22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"/>',
      files: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6"/>',
      upload: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12"/>',
      'alert-triangle': '<path d="m10.3 3.9-8.5 14.2A2 2 0 0 0 3.5 21h17a2 2 0 0 0 1.7-2.9L13.7 3.9a2 2 0 0 0-3.4 0ZM12 9v4M12 17h.01"/>'
    }
    return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || ''}</svg>`
  }
  function mount(container, options = {}) {
    const request = options.request || ((...args) => fetch(...args))
    const showToast = options.toast || (() => {})
    const icon = options.icon || defaultIcon
    const RAG_MANAGE = !!options.manage, RAG_SCOPED = !!options.scoped
    const timers = new Set()
    let disposed = false, creatingCollection = false
    const active = () => !disposed && container.isConnected
    const getById = id => container.querySelector('#' + id)
    const onKeyDown = event => { if (event.key === 'Escape') closeModal() }
    const later = (callback, delay) => {
      const timer = setTimeout(() => { timers.delete(timer); if (active()) callback() }, delay)
      timers.add(timer)
    }
    container.classList.add('prism-documents')
    let currentCollection = null
    let collections = []
    let documentsRequestVersion = 0, collectionsRequestVersion = 0, displayedDocuments = null
    async function requireRAGSuccess(response) {
      if (!response.ok) {
        const raw = await response.text()
        let message = raw.trim()
        try { const data = JSON.parse(raw); message = data.error || data.message || message } catch (_) {}
        throw new Error(message || 'Request failed (' + response.status + ')')
      }
      return response
    }

    async function renderRAGTab(container) {
      // Check RAG status first
      try {
        const res = await request('/api/rag/status')
        const data = await res.json()
        if (!active()) return
        if (!data.ready) {
          const msg = document.createElement('div')
          msg.style.cssText = 'padding:40px 24px;color:var(--text3);font-size:13px'
          msg.textContent = 'RAG not ready: ' + (data.status || 'initializing…')
          container.appendChild(msg)
          // Update the existing notice instead of clearing the whole tab on each poll.
          const poll = async () => {
            if (!active() || !msg.isConnected) return
            try {
              const response = await request('/api/rag/status')
              if (!response.ok) throw new Error('Status unavailable')
              const next = await response.json()
              if (!active() || !msg.isConnected) return
              if (next.ready) {
                container.replaceChildren()
                await renderRAGTab(container)
                return
              }
              const text = 'RAG not ready: ' + (next.status || 'initializing…')
              if (msg.textContent !== text) msg.textContent = text
            } catch { /* Keep the current notice during a transient network failure. */ }
            if (active() && msg.isConnected) later(poll, 2000)
          }
          later(poll, 2000)
          return
        }
      } catch { /* ignore, render anyway */ }

      if (!active()) return
      const tab = document.createElement('div')
      tab.id = 'rag-tab'
      container.appendChild(tab)

      // Sidebar
      const sidebar = document.createElement('aside')
      sidebar.id = 'rag-sidebar'
      sidebar.innerHTML = `
        <div id="rag-sidebar-header">
          <span id="rag-sidebar-title">Collections</span>
          ${RAG_MANAGE ? '<button id="new-collection-btn" title="New collection" data-new-collection>+</button>' : ''}
        </div>
        <div id="collections-list"></div>
        ${RAG_SCOPED
          ? (RAG_MANAGE ? '' : '<div style="padding:10px 12px;color:var(--text3);font-size:11.5px">Group knowledge base — managed by your group admin (read-only).</div>')
          : '<div style="padding:10px 12px;color:var(--text3);font-size:11.5px">You\'re not part of a group yet — ask an admin to add you to one to access shared knowledge.</div>'}`
      tab.appendChild(sidebar)

      // Content
      const content = document.createElement('div')
      content.id = 'rag-content'
      content.innerHTML = `<div id="no-collection-msg"><div class="icon">${icon('knowledge',40)}</div><div>Select a collection or create a new one</div></div>`
      tab.appendChild(content)
      const dialog = document.createElement('div')
      dialog.innerHTML = `<div id="modal-overlay">
      <div id="modal">
        <h3>New collection</h3>
        <div>
          <label for="modal-input">Name</label>
          <input id="modal-input" type="text" placeholder="e.g. my-docs" maxlength="60">
          <div id="modal-hint">Letters, numbers, hyphens. Will be lowercased.</div>
        </div>
        <div>
          <label for="modal-desc">Description — tells the agent when to search this collection</label>
          <textarea id="modal-desc" maxlength="300" rows="3"
                    placeholder="e.g. Internal API documentation: endpoints, authentication, error codes."></textarea>
        </div>
        <div class="modal-actions">
          <button class="btn" id="collection-cancel">Cancel</button>
          <button class="btn btn-primary" id="collection-create">Create</button>
        </div>
      </div>
    </div>`
      container.appendChild(dialog)
      getById('modal-overlay').addEventListener('click', closeModal)
      getById('collection-cancel').addEventListener('click', () => closeModal())
      getById('collection-create').addEventListener('click', createCollection)
      getById('modal-input').addEventListener('keydown', event => { if (event.key === 'Enter') { event.preventDefault(); getById('modal-desc').focus() } })
      getById('modal-desc').addEventListener('keydown', event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); createCollection() } })
      container.addEventListener('keydown', onKeyDown)
      container.querySelectorAll('[data-new-collection]').forEach(button => button.addEventListener('click', openModal))

      if (RAG_SCOPED) await loadCollections()
    }

    async function loadCollections() {
      const version = ++collectionsRequestVersion
      try {
        const res = await requireRAGSuccess(await request('/api/rag/collections'))
        const data = await res.json()
        if (!Array.isArray(data)) throw new Error('Invalid collection list')
        if (!active() || version !== collectionsRequestVersion) return
        collections = data
        renderCollections()
      } catch(e) { if (version === collectionsRequestVersion) showToast('Could not load collections: ' + e.message, true) }
    }

    function renderCollections() {
      const el = getById('collections-list')
      if (!el) return
      if (collections.length === 0) {
        el.innerHTML = '<div style="padding:20px 16px;font-size:12px;color:var(--text3);text-align:center">No collections</div>'
        return
      }
      el.innerHTML = ''
      for (const c of collections) {
        const item = document.createElement('div')
        item.className = 'collection-item' + (c.name === currentCollection ? ' active' : '')
        item.innerHTML = `
          <span class="collection-name">${escHtml(c.name)}</span>
          <span class="collection-count">${c.doc_count} doc${c.doc_count !== 1 ? 's' : ''}</span>
          ${RAG_MANAGE ? '<button class="collection-del" title="Delete">✕</button>' : ''}`
        item.addEventListener('click', () => selectCollection(c.name))
        item.querySelector('.collection-del')?.addEventListener('click', e => {
          e.stopPropagation(); deleteCollection(c.name)
        })
        el.appendChild(item)
      }
    }

    function selectCollection(name) {
      documentsRequestVersion++; displayedDocuments = null
      currentCollection = name
      renderCollections()
      renderRAGContent()
      loadDocuments()
    }

    async function deleteCollection(name) {
      if (!await PrismModal.confirm(`Delete collection "${name}" and all its documents?`, { danger: true })) return
      try {
        await requireRAGSuccess(await request(`/api/rag/collections?name=${encodeURIComponent(name)}`, { method: 'DELETE' }))
        if (!active()) return
        if (currentCollection === name) {
          documentsRequestVersion++; displayedDocuments = null
          currentCollection = null
        const content = getById('rag-content')
        if (content) content.innerHTML = `<div id="no-collection-msg"><div class="icon">${icon('knowledge',40)}</div><div>Select a collection or create a new one</div></div>`
        }
        await loadCollections()
      } catch (e) { showToast('Could not delete collection: ' + e.message, true) }
    }

    // Le <select> de la zone d'upload reflète la collection courante ; le choisir ici
    // revient à la sélectionner dans la sidebar (une seule source de vérité :
    // currentCollection). L'option « + New collection… » ouvre notre modale, pas une
    // boîte de dialogue du navigateur.
    function syncUploadTarget() {
      const sel = getById('upload-collection')
      if (!sel) return
      sel.innerHTML = collections
        .map(c => `<option value="${escHtml(c.name)}"${c.name === currentCollection ? ' selected' : ''}>${escHtml(c.name)}</option>`)
        .join('') + '<option value="__new__">+ New collection…</option>'
    }

    function onUploadTargetChange(value) {
      if (value === '__new__') { openModal(); return }
      if (value && value !== currentCollection) selectCollection(value)
    }

    function renderRAGContent() {
      const collection = currentCollection
      const col = collections.find(c => c.name === collection)
      const desc = col ? (col.description || '') : ''
      const content = getById('rag-content')
      if (!content) return
      content.innerHTML = `
        <div id="rag-content-header">
          <div style="display:flex;flex-direction:column;gap:4px;flex:1;min-width:0">
            <span style="font-size:14px;font-weight:600;color:var(--text)">${icon('files',15)} ${escHtml(currentCollection)}</span>
            ${RAG_MANAGE ? `<input id="desc-input" type="text" value="${escHtml(desc)}"
                   class="${desc ? '' : 'missing'}"
                   placeholder="Description for the agent (e.g. internal API docs…)"
                   style="background:transparent;border:none;border-bottom:1px dashed var(--border2);
                          color:var(--text3);font-size:11.5px;font-family:var(--sans);
                          outline:none;padding:2px 0;width:100%">
                 ${desc ? '' : `<div id="desc-warn">${icon('alert-triangle',11)} No description — the agent won't know when to search this collection.</div>`}`
              : `<span style="color:var(--text3);font-size:11.5px">${escHtml(desc || 'No description')}</span>`}
          </div>
        </div>
        ${RAG_MANAGE ? `<div id="upload-target">
          <span>Add to</span>
          <select id="upload-collection"></select>
          <button type="button" data-new-collection>+ New collection</button>
        </div>
        <div id="upload-zone">
          <div id="upload-icon">${icon('upload',26)}</div>
          <div id="upload-label">Drop a file here or click to select</div>
          <div id="upload-hint">PDF, DOCX, TXT, CSV, MD, JSON — max 50 MB</div>
          <input id="upload-input" type="file" multiple accept=".pdf,.docx,.txt,.csv,.md,.json,.tsv"
    >
        </div>` : ''}
        <div id="upload-progress"></div>
        <div id="docs-area"><div style="padding:20px;color:var(--text3);font-size:13px">Loading…</div></div>`

      if (RAG_MANAGE) {
        syncUploadTarget()
        getById('upload-collection').addEventListener('change', event => onUploadTargetChange(event.target.value))
        getById('upload-input').addEventListener('change', event => uploadFiles(event.target.files))
        content.querySelectorAll('[data-new-collection]').forEach(button => button.addEventListener('click', openModal))
        getById('upload-zone').addEventListener('click', () => getById('upload-input').click())
        const zone = getById('upload-zone')
        zone.addEventListener('dragover', e => { e.preventDefault(); zone.classList.add('drag-over') })
        zone.addEventListener('dragleave', () => zone.classList.remove('drag-over'))
        zone.addEventListener('drop', e => { e.preventDefault(); zone.classList.remove('drag-over'); uploadFiles(e.dataTransfer.files) })
        const di = getById('desc-input')
        di.addEventListener('blur', () => saveDescription(di.value.trim(), collection))
        di.addEventListener('keydown', e => { if (e.key === 'Enter') di.blur() })
      }
    }

    async function saveDescription(value, collection = currentCollection) {
      const col = collections.find(c => c.name === collection)
      if (!col) return
      const controls = collection === currentCollection ? [getById('desc-input')].filter(Boolean) : []
      try {
        await requireRAGSuccess(await request('/api/rag/collections', {
          method: 'PATCH', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: collection, description: value })
        }, controls))
        col.description = value
      } catch (e) { showToast('Could not save description: ' + e.message, true) }
    }

    async function loadDocuments() {
      const collection = currentCollection, area = getById('docs-area')
      if (!collection || !area) return
      const version = ++documentsRequestVersion
      displayedDocuments = null
      area.querySelectorAll('.doc-del-btn').forEach(button => button.disabled = true)
      try {
        const res = await requireRAGSuccess(await request(`/api/rag/documents?collection=${encodeURIComponent(collection)}`))
        const docs = await res.json()
        if (!active() || version !== documentsRequestVersion || collection !== currentCollection || area !== getById('docs-area')) return
        if (!Array.isArray(docs)) throw new Error('Invalid document list')
        displayedDocuments = { collection, area, ids: new Set(docs.map(doc => Number(doc.id))), deleting: new Set() }
        renderDocuments(docs)
      } catch(e) {
        if (!active() || version !== documentsRequestVersion || collection !== currentCollection || area !== getById('docs-area')) return
        showToast('Failed to load documents: ' + e.message, true)
        if (!area.querySelectorAll('.doc-del-btn').length) area.textContent = 'Could not load documents. Select the collection to retry.'
      }
    }

    function renderDocuments(docs) {
      const area = getById('docs-area')
      if (!area) return
      if (docs.length === 0) {
        area.innerHTML = '<div style="padding:24px 0;text-align:center;font-size:13px;color:var(--text3)">No documents in this collection.<br>Drop a file in the zone above.</div>'
        return
      }
      area.innerHTML = `
        <table id="docs-table">
          <thead><tr><th>File</th><th>Size</th><th>Chunks</th><th>Updated</th><th></th></tr></thead>
          <tbody>${docs.map(d => `
            <tr>
              <td>${escHtml(d.filename)}</td>
              <td>${fmtSize(d.size_bytes)}</td>
              <td>${d.chunk_count}</td>
              <td>${fmtDate(d.updated_at)}</td>
              <td>${RAG_MANAGE ? `<button class="doc-del-btn" title="Delete" data-id="${d.id}">✕</button>` : ''}</td>
            </tr>`).join('')}
          </tbody>
        </table>`
      const snapshot = displayedDocuments
      area.querySelectorAll('.doc-del-btn').forEach(btn => {
        btn.addEventListener('click', () => deleteDocument(Number(btn.dataset.id), snapshot))
      })
    }

    async function deleteDocument(id, snapshot = displayedDocuments) {
      if (!snapshot || snapshot !== displayedDocuments || snapshot.collection !== currentCollection ||
          snapshot.area !== getById('docs-area') || !snapshot.ids.has(id) || snapshot.deleting.has(id)) return
      snapshot.deleting.add(id)
      const button = [...snapshot.area.querySelectorAll('.doc-del-btn')].find(btn => Number(btn.dataset.id) === id)
      if (button) button.disabled = true
      try {
        await requireRAGSuccess(await request(`/api/rag/document?id=${encodeURIComponent(id)}`, { method: 'DELETE' }))
        if (snapshot === displayedDocuments) await loadDocuments()
        await loadCollections()
      } catch (e) { showToast('Could not delete document: ' + e.message, true) }
      finally {
        snapshot.deleting.delete(id)
        if (snapshot === displayedDocuments && button) button.disabled = false
      }
    }

    // L'upload est un POST synchrone qui parse, embedde et écrit avant de répondre —
    // des minutes sur un gros manuel. On interroge /api/rag/upload/progress pendant
    // que le POST est en vol pour dire où on en est.
    function fmtETA(s) { if (!s || s < 1) return ''; if (s < 60) return `~${s}s left`
      const m = Math.floor(s / 60); return `~${m}m${s % 60 ? ' ' + (s % 60) + 's' : ''} left` }

    async function pollIngestProgress(collection, filename, progress, stop) {
      while (!stop.done && active()) {
        await new Promise(r => setTimeout(r, 700))
        if (stop.done || !active()) return
        let d
        try {
          const res = await request('/api/rag/upload/progress'
            + `?collection=${encodeURIComponent(collection)}&filename=${encodeURIComponent(filename)}`)
          d = await res.json()
        } catch (_) { continue }
        if (!d || !d.found) continue
        if (d.stage === 'parsing') progress.textContent = `Parsing ${filename}…`
        else if (d.stage === 'embedding') {
          progress.textContent = d.done
            ? `Indexing ${d.done}/${d.total} chunks (${Math.round(d.rate)}/s) ${fmtETA(d.etaSecs)}`
            : `Indexing ${d.total} chunks…`
        } else if (d.stage === 'storing') progress.textContent = `Saving ${d.total} chunks…`
        else if (d.stage === 'failed') { progress.textContent = `Failed: ${d.error || 'unknown error'}`; return }
        else if (d.stage === 'done') { progress.textContent = `${d.total} chunks indexed ✓`; return }
      }
    }

    async function uploadFiles(files) {
      if (!RAG_MANAGE || !files || files.length === 0) return
      const sel = getById('upload-collection')
      if (sel && sel.value && sel.value !== '__new__' && sel.value !== currentCollection) {
        selectCollection(sel.value)   // l'utilisateur a changé de cible sans quitter la zone
      }
      if (!currentCollection) { showToast('Select a collection first', true); return }
      const collection = currentCollection, input = getById('upload-input')
      const zone = getById('upload-zone')
      const progress = getById('upload-progress')
      zone.classList.add('uploading'); progress.classList.add('visible')
      for (let i = 0; i < files.length; i++) {
        const file = files[i]
        progress.textContent = `Uploading ${i+1}/${files.length}: ${file.name}…`
        try {
          const form = new FormData()
          form.append('file', file); form.append('collection', collection)
          const stop = { done: false }
          pollIngestProgress(collection, file.name, progress, stop)
          let res
          try { res = await request('/api/rag/upload', { method: 'POST', body: form }) }
          finally { stop.done = true }
          const data = await res.json()
          if (!res.ok) throw new Error(data.error || res.statusText)
          showToast(`✓ ${file.name} — ${data.chunks} chunks indexed`)
        } catch(e) { showToast(`Error ${file.name}: ${e.message}`, true) }
      }
      zone.classList.remove('uploading'); progress.classList.remove('visible'); progress.textContent = ''
      input.value = ''
      if (active() && currentCollection === collection) await loadDocuments()
      if (active()) await loadCollections()
    }

    // ── Modal ─────────────────────────────────────────────────────────────────────

    function openModal() {
      if (!RAG_MANAGE) return
      getById('modal-overlay').classList.add('visible')
      later(() => getById('modal-input').focus(), 50)
    }
    function closeModal(e) {
      if (e && e.target !== getById('modal-overlay')) return
      getById('modal-overlay').classList.remove('visible')
      getById('modal-input').value = ''
      getById('modal-desc').value = ''
      syncUploadTarget()   // le <select> avait pu basculer sur « + New collection… »
    }
    async function createCollection() {
      if (!RAG_MANAGE || creatingCollection) return
      const input = getById('modal-input')
      const descEl = getById('modal-desc')
      const name = input.value.trim().toLowerCase().replace(/[^a-z0-9\-_]/g,'-').replace(/-+/g,'-')
      if (!name) { input.focus(); return }
      const description = descEl.value.trim()

      // Persist the collection (and its description) so it survives with zero documents.
      // The description is what the agent reads to decide when to search here — see the
      // "## Knowledge Base (RAG)" block built in internal/server/ws.go.
      creatingCollection = true
      const button = getById('collection-create')
      button.disabled = true
      try {
        await requireRAGSuccess(await request('/api/rag/collections', {
          method: 'PATCH', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name, description }),
        }))
      } catch (e) { showToast('Could not create collection: ' + e.message, true); return }
      finally { creatingCollection = false; button.disabled = false }
      if (!active()) return
      getById('modal-overlay').classList.remove('visible')
      input.value = ''; descEl.value = ''
      if (!collections.find(c => c.name === name)) {
        collections.push({ name, description, doc_count: 0, chunk_count: 0 })
        renderCollections()
      }
      selectCollection(name)
    }

    // ── Utils ─────────────────────────────────────────────────────────────────────

    function fmtSize(b) {
      if (b < 1024) return b + ' B'
      if (b < 1048576) return (b/1024).toFixed(1) + ' KB'
      return (b/1048576).toFixed(1) + ' MB'
    }
    function fmtDate(iso) {
      if (!iso) return ''
      return new Date(iso).toLocaleDateString('en-GB', { day:'2-digit', month:'2-digit', year:'numeric' })
    }


    return {
      ready: renderRAGTab(container),
      destroy() {
        disposed = true
        documentsRequestVersion++; collectionsRequestVersion++; displayedDocuments = null
        container.removeEventListener('keydown', onKeyDown)
        timers.forEach(clearTimeout); timers.clear()
      }
    }
  }
  async function permissions(request = (...args) => fetch(...args)) {
    const me = await requireResponse(await request('/api/me')).then(response => response.json())
    const multiUser = !!me.multiUser
    if (!me.authenticated) return {multiUser, manage: false, scoped: false}
    const membership = await requireResponse(await request('/api/my/groups')).then(response => response.json())
    const inGroup = (membership.groups || []).length > 0
    return {multiUser, manage: !multiUser && !inGroup, scoped: inGroup || !multiUser}
  }
  async function requireResponse(response) {
    if (!response.ok) throw new Error('Could not load account permissions')
    return response
  }
  return {mount, permissions}
})()
