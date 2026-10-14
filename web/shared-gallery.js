// Group publications use static, access-controlled previews. Browsing the
// gallery never runs another member's widget code or its backend tools.
export function createSharedGallery({ getGroups, getSession, getWidgets, getMe, showToast }) {
  function el(tag, cls, text) {
    const node = document.createElement(tag)
    if (cls) node.className = cls
    if (text !== undefined) node.textContent = text
    return node
  }
  async function request(url, options) {
    const r = await fetch(url, options)
    const data = await r.json().catch(() => ({}))
    if (!r.ok) throw new Error(data.error || `Request failed (${r.status})`)
    return data
  }
  function notify(title, message, level = 'success') { showToast({ title, message, level }) }
  function shareFields(container) {
    const groups = getGroups()
    let picker
    if (groups.length > 1) {
      const label = el('label', 'shared-field', 'Group')
      picker = el('select')
      for (const g of groups) {
        const opt = el('option', '', g.groupName); opt.value = g.groupId; picker.append(opt)
      }
      label.append(picker); container.append(label)
    } else container.append(el('p', 'shared-muted', `Shared with ${groups[0].groupName}.`))
    const label = el('label', 'shared-field', 'Description (optional)')
    const description = el('textarea'); description.rows = 3; description.maxLength = 1000
    description.placeholder = 'What does it do, and when is it useful?'
    label.append(description); container.append(label)
    container.append(el('p', 'shared-muted', 'Includes a snapshot of the visible widget. For a dashboard, the first widget is used as its cover.'))
    return () => ({ groupId: picker ? Number(picker.value) : groups[0].groupId, description: description.value.trim() })
  }
  async function publish(body) {
    const result = await request('/api/shared', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body), signal: AbortSignal.timeout(40000),
    })
    notify('Shared', `${result.count} widget(s) published to the group gallery.${result.warning ? ' ' + result.warning : ''}`)
  }
  async function shareWidget(id, title) {
    if (!getGroups().length || !window.PrismModal) return
    const session = getSession()
    await PrismModal.open({
      render(box, done) {
        box.classList.add('shared-share-dialog')
        box.append(el('div', 'pm-title', `Share “${title}”`))
        const read = shareFields(box)
        const status = el('div', 'shared-status'); status.setAttribute('role', 'status')
        const foot = el('div', 'pm-foot')
        const cancel = PrismModal.btn('Cancel', '', () => done())
        const submit = PrismModal.btn('Share', 'pm-primary', async () => {
          submit.disabled = true; cancel.disabled = true; submit.textContent = 'Sharing…'; status.textContent = ''
          try { await publish({ kind: 'widget', widgetId: id, session, ...read() }); done() }
          catch (e) { status.textContent = e.message }
          finally { submit.disabled = false; cancel.disabled = false; submit.textContent = 'Share' }
        })
        foot.append(cancel, submit); box.append(status, foot)
      },
    })
  }
  function showPreview(item) {
    PrismModal.open({ render(box, done) {
      box.classList.add('shared-preview-dialog')
      box.append(el('div', 'pm-title', item.title))
      const image = el('img', 'shared-preview-large'); image.src = `/api/shared/${item.id}/preview`; image.alt = item.title
      box.append(image)
      if (item.description) box.append(el('p', 'shared-description', item.description))
      const foot = el('div', 'pm-foot'); foot.append(PrismModal.btn('Close', '', () => done())); box.append(foot)
    } })
  }
  function openSharedGallery() {
    if (!window.PrismModal) return
    let tab = 'add', generation = 0
    PrismModal.open({ render(box, done) {
      box.classList.add('shared-gallery-dialog')
      const head = el('div', 'shared-gallery-head')
      head.append(el('div', 'pm-title', 'Shared widgets'))
      const add = PrismModal.btn('Browse', '', () => { tab = 'add'; render() })
      const share = PrismModal.btn('Share', '', () => { tab = 'share'; render() })
      head.append(add, share)
      const body = el('div', 'shared-gallery-body')
      const foot = el('div', 'pm-foot'); foot.append(PrismModal.btn('Close', '', () => done()))
      box.append(head, body, foot)
      function render() {
        const version = ++generation
        add.className = 'pm-btn' + (tab === 'add' ? ' pm-primary' : '')
        share.className = 'pm-btn' + (tab === 'share' ? ' pm-primary' : '')
        body.replaceChildren()
        if (tab === 'add') renderSharedAdd(body, () => version === generation && box.isConnected)
        else renderSharedShare(body)
      }
      render()
    } })
  }
  async function renderSharedAdd(body, active) {
    body.replaceChildren(el('p', 'shared-empty', 'Loading…'))
    let items
    try { items = (await request('/api/shared')).items || [] }
    catch (e) { if (active()) body.replaceChildren(el('p', 'shared-empty', e.message)); return }
    if (!active()) return
    body.replaceChildren()
    if (!items.length) { body.append(el('p', 'shared-empty', 'Nothing shared in your group yet.')); return }
    const selected = new Set(), buttons = new Map()
    const toolbar = el('div', 'shared-gallery-toolbar')
    const bulk = PrismModal.btn('Add selected', 'pm-primary', () => addItems([...selected], bulk))
    bulk.hidden = true; toolbar.append(bulk)
    const grid = el('div', 'shared-gallery-grid')
    body.append(toolbar, grid)
    async function addItems(ids, button) {
      const session = getSession() // Pin the destination even if the user changes workspace during the request.
      button.disabled = true
      let count = 0, failures = []
      for (const id of ids) {
        const own = buttons.get(id); if (own) own.disabled = true
        try {
          const result = await request(`/api/shared/${id}/add?session=${encodeURIComponent(session)}`, { method: 'POST' })
          if (!result.added) throw new Error('No widgets could be added.')
          count += result.added
          if (own) own.textContent = 'Added ✓'
          selected.delete(id)
          const checkbox = grid.querySelector(`[data-share-id="${id}"]`); if (checkbox) checkbox.checked = false
        } catch (e) { failures.push(e.message); if (own) own.disabled = false }
      }
      bulk.hidden = !selected.size; bulk.textContent = `Add selected (${selected.size})`; bulk.disabled = false
      if (failures.length) { button.disabled = false; notify('Some widgets could not be added', `${count} added. ${failures.join(' ')}`, 'error') }
      else notify('Added', `${count} widget(s) added to this workspace`)
    }
    for (const item of items) {
      const card = el('article', 'shared-card')
      const preview = el(item.hasPreview ? 'button' : 'div', 'shared-card-preview')
      if (item.hasPreview) {
        preview.type = 'button'; preview.title = 'Enlarge preview'; preview.setAttribute('aria-label', `Preview ${item.title}`)
        const img = el('img'); img.src = `/api/shared/${item.id}/preview`; img.alt = item.title; img.loading = 'lazy'
        img.onerror = () => { preview.replaceChildren(el('span', 'shared-muted', 'Preview unavailable')); preview.disabled = true }
        preview.append(img); preview.onclick = () => showPreview(item)
      } else preview.append(el('span', 'shared-muted', 'No preview available'))
      const info = el('div', 'shared-card-info')
      const heading = el('div', 'shared-card-heading')
      const label = el('label', 'shared-card-select')
      const checkbox = el('input', 'shared-check'); checkbox.type = 'checkbox'; checkbox.dataset.shareId = item.id
      checkbox.setAttribute('aria-label', `Select ${item.title}`)
      checkbox.onchange = () => {
        checkbox.checked ? selected.add(item.id) : selected.delete(item.id)
        bulk.textContent = `Add selected (${selected.size})`; bulk.hidden = !selected.size
      }
      label.append(checkbox, el('span', 'shared-card-title', item.title)); heading.append(label)
      if (item.ownerId === getMe().uid || getMe().isAdmin) {
        const del = PrismModal.btn('✕', '', async () => {
          if (!await PrismModal.confirm(`Remove “${item.title}” from the gallery? Copies already added to workspaces are kept.`, { title: 'Unshare', okLabel: 'Remove' })) return
          del.disabled = true
          try { await request(`/api/shared/${item.id}`, { method: 'DELETE' }); renderSharedAdd(body, active) }
          catch (e) { del.disabled = false; notify('Unshare failed', e.message, 'error') }
        })
        del.classList.add('shared-remove'); del.title = 'Remove from the shared gallery'; del.setAttribute('aria-label', `Unshare ${item.title}`); heading.append(del)
      }
      info.append(heading, el('div', 'shared-muted', `by ${item.ownerName || '—'} · ${item.groupName || ''}`))
      if (item.kind === 'dashboard') info.append(el('div', 'shared-kind', `Dashboard · ${item.count} widgets · first widget preview`))
      info.append(el('p', 'shared-description', item.description || 'No description provided.'))
      const add = PrismModal.btn('Add to workspace', '', () => addItems([item.id], add))
      buttons.set(item.id, add); info.append(add); card.append(preview, info); grid.append(card)
    }
  }
  function renderSharedShare(body) {
    if (!getGroups().length) { body.append(el('p', 'shared-empty', 'You are not in any group.')); return }
    const session = getSession()
    const form = el('div', 'shared-share-form')
    const label = el('label', 'shared-field', 'What to share'), picker = el('select')
    const all = el('option', '', 'Whole dashboard (all widgets)'); all.value = '__all__'; picker.append(all)
    for (const widget of getWidgets().values()) { const opt = el('option', '', widget.title); opt.value = widget.id; picker.append(opt) }
    label.append(picker); form.append(label)
    const read = shareFields(form), status = el('div', 'shared-status'); status.setAttribute('role', 'status')
    const submit = PrismModal.btn('Share', 'pm-primary', async () => {
      submit.disabled = true; submit.textContent = 'Sharing…'; status.textContent = ''
      try {
        const kind = picker.value === '__all__' ? 'dashboard' : 'widget'
        await publish({ kind, widgetId: kind === 'widget' ? picker.value : undefined, session, ...read() })
        status.textContent = 'Published to the group gallery.'
      } catch (e) { status.textContent = e.message }
      finally { submit.disabled = false; submit.textContent = 'Share' }
    })
    form.append(status, submit); body.append(form)
  }
  window.openSharedGallery = openSharedGallery
  return { shareWidget, openSharedGallery }
}
