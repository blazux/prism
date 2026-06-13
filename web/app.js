// Dashboard — Frontend

// ─── State ───────────────────────────────────────────────────────────────────

let ws = null
let isStreaming = false
let chatOpen = false
let currentAssistantEl = null
let currentAssistantContent = ''
const widgets = new Map()
let grid = null
let pendingImages = []      // base64 strings (without data-URL prefix) waiting to be sent
let pendingAttachments = [] // images queued by add_attachment, injected into next assistant bubble
let pendingFiles  = [] // {name, text} parsed file attachments waiting to be sent
let currentSessionID = localStorage.getItem('active-session') || 'default'

function updateSettingsLink() {
  const link = document.getElementById('settings-link')
  if (link) link.href = `/settings.html?session=${encodeURIComponent(currentSessionID)}#tools`
}
let sessionMenuOpen = false
let batchLoading = false
let batchLoadingTimer = null
let notifications = []   // [{id, title, message, level, read, createdAt}]
let notifPanelOpen = false

const layoutKey = () => `dashboard-layout:${currentSessionID}`

// ─── GridStack ────────────────────────────────────────────────────────────────

function initGrid() {
  grid = GridStack.init({
    column: 12,
    cellHeight: 25,
    handle: '.widget-header',
    margin: 6,
    float: false,
    disableOneColumnMode: true,
  }, '#widget-grid')
  grid.on('change', saveLayout)

  // Prevent iframes from swallowing mouse events during drag/resize
  grid.on('dragstart resizestart', () => {
    document.querySelectorAll('.widget-body iframe').forEach(f => { f.style.pointerEvents = 'none' })
  })
  grid.on('dragstop resizestop', () => {
    document.querySelectorAll('.widget-body iframe').forEach(f => { f.style.pointerEvents = '' })
  })
}

function saveLayout() {
  if (!grid || batchLoading) return
  const nodes = grid.save(false)
  const layout = {}
  nodes.forEach(n => { if (n.id) layout[n.id] = { x: n.x, y: n.y, w: n.w, h: n.h } })
  localStorage.setItem(layoutKey(), JSON.stringify(layout))
}

function loadWidgetPos(elemId) {
  try { return JSON.parse(localStorage.getItem(layoutKey()) || '{}')[elemId] ?? null }
  catch { return null }
}

// ─── WebSocket ────────────────────────────────────────────────────────────────

function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/ws?session=${encodeURIComponent(currentSessionID)}`)

  ws.onopen  = () => { clearChat(); batchLoading = true }
  ws.onclose = () => { setContainerBadge('unknown'); setTimeout(connect, 2000) }
  ws.onerror = () => {}
  ws.onmessage = (e) => {
    try { handleServerMsg(JSON.parse(e.data)) }
    catch(err) { console.error('[ws] message error:', err) }
  }
}

function send(obj) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj))
}

// ─── Server messages ──────────────────────────────────────────────────────────

function handleServerMsg(msg) {
  switch (msg.type) {
    case 'stream':          appendStream(msg.content); break
    case 'stream_end':      finalizeStream(); break
    case 'attachment':      pendingAttachments.push(...(msg.images || [])); break
    case 'tool_use':        appendToolUse(msg); break
    case 'tool_result':     appendToolResult(msg); break
    case 'plugin_load':
      addWidget(msg.id, msg.title, msg.content, msg.cols, msg.height, msg.locked)
      clearTimeout(batchLoadingTimer)
      batchLoadingTimer = setTimeout(() => { batchLoading = false }, 0)
      break
    case 'plugin_unload':   removeWidget(msg.id); break
    case 'container_status':
      setContainerBadge(msg.status)
      if (msg.model) setCurrentModel(msg.model)
      if (msg.sessionID) { currentSessionID = msg.sessionID; loadSessions() }
      break
    case 'model_set':       setCurrentModel(msg.model); break
    case 'chat_reset':             clearChat(); break
    case 'chat_history':           restoreChatHistory(msg.messages); break
    case 'notification':           receiveNotification(msg); break
    case 'notifications_history':  loadNotificationsHistory(msg.notifications); break
    case 'notifications_read':     markNotificationsReadLocal(); break
    case 'notification_deleted':   removeNotificationLocal(msg.id); break
    case 'error':           appendError(msg.content); break
    case 'tools_list':
    case 'tools_updated':
    case 'mcp_updated':
      break
    case 'secret_request': showSecretDialog(msg.name, msg.description); break
    case 'open_file':    break  // handled via file_content callback
    case 'file_content': openEditor(msg.path, msg.content); break
    case 'saved':       editorOnSaved(msg.path); break
    case 'file_tree': case 'file_changed': break
  }
}

// ─── Dashboard ────────────────────────────────────────────────────────────────

function addWidget(id, title, content, cols, height, locked) {
  cols   = Math.max(1, Math.min(3, cols   || 1))
  height = height > 0 ? height : 280

  // Map agent cols (1-3) → GridStack width (out of 12 columns)
  const gsW    = cols * 4
  const gsH    = Math.max(4, Math.round(height / 25))
  const elemId = 'widget-' + id

  // Remove existing widget with same id (update flow)
  const existing = document.getElementById(elemId)
  if (existing) grid.removeWidget(existing, true)

  // Build widget DOM
  const el = document.createElement('div')
  el.className = 'grid-stack-item'
  el.id = elemId

  const inner = document.createElement('div')
  inner.className = 'grid-stack-item-content'

  const card = document.createElement('div')
  card.className = 'widget-card'

  const hdr = document.createElement('div')
  hdr.className = 'widget-header'

  const titleEl = document.createElement('span')
  titleEl.className = 'widget-title'
  titleEl.textContent = title

  const lockBtn = document.createElement('button')
  lockBtn.className = 'widget-lock'
  lockBtn.title = locked ? 'Unlock' : 'Lock'
  lockBtn.textContent = locked ? '🔒' : '🔓'
  lockBtn.addEventListener('click', () => {
    const isLocked = lockBtn.textContent === '🔒'
    const nowLocked = !isLocked
    lockBtn.textContent = nowLocked ? '🔒' : '🔓'
    lockBtn.title = nowLocked ? 'Unlock' : 'Lock'
    closeBtn.style.display = nowLocked ? 'none' : ''
    widgets.set(id, { ...widgets.get(id), locked: nowLocked })
    send({ type: 'lock_plugin', id, locked: nowLocked })
  })

  const closeBtn = document.createElement('button')
  closeBtn.className = 'widget-close'
  closeBtn.textContent = '×'
  closeBtn.title = 'Remove'
  closeBtn.style.display = locked ? 'none' : ''
  closeBtn.addEventListener('click', () => send({ type: 'remove_plugin', id }))

  hdr.appendChild(titleEl)
  hdr.appendChild(lockBtn)
  hdr.appendChild(closeBtn)

  const body = document.createElement('div')
  body.className = 'widget-body'

  const iframe = document.createElement('iframe')
  iframe.srcdoc = content
  iframe.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms allow-popups allow-downloads allow-modals allow-popups-to-escape-sandbox')

  body.appendChild(iframe)
  card.appendChild(hdr)
  card.appendChild(body)
  inner.appendChild(card)
  el.appendChild(inner)

  // Restore saved position or auto-place via gs-* attributes
  const saved = loadWidgetPos(elemId)
  el.setAttribute('gs-w',  String(saved?.w ?? gsW))
  el.setAttribute('gs-h',  String(saved?.h ?? gsH))
  el.setAttribute('gs-id', elemId)
  if (saved?.x !== undefined) el.setAttribute('gs-x', String(saved.x))
  if (saved?.y !== undefined) el.setAttribute('gs-y', String(saved.y))

  try {
    grid.addWidget(el)
  } catch(err) {
    console.error('[addWidget] GridStack error:', err)
  }
  widgets.set(id, { cols, height, locked: !!locked })
  updateEmptyState()
}

function removeWidget(id) {
  const el = document.getElementById('widget-' + id)
  if (el) {
    grid.removeWidget(el, true)
    updateEmptyState()
  }
  widgets.delete(id)
}

function updateEmptyState() {
  document.getElementById('empty-state').style.display = widgets.size > 0 ? 'none' : 'flex'
}

// ─── Chat drawer ──────────────────────────────────────────────────────────────

window.toggleChat = function() {
  chatOpen = !chatOpen
  document.getElementById('chat-drawer').classList.toggle('open', chatOpen)
  document.getElementById('chat-overlay').classList.toggle('visible', chatOpen)
  document.getElementById('chat-fab').classList.toggle('active', chatOpen)
  if (chatOpen) setTimeout(() => document.getElementById('chat-input').focus(), 50)
}

document.getElementById('chat-overlay').addEventListener('click', () => {
  if (chatOpen) toggleChat()
})

document.querySelectorAll('.empty-examples span').forEach(el => {
  el.addEventListener('click', () => {
    document.getElementById('chat-input').value = el.dataset.prompt
    if (!chatOpen) toggleChat()
    document.getElementById('chat-input').focus()
  })
})

// ─── Chat UI ─────────────────────────────────────────────────────────────────

function sendChat() {
  const input = document.getElementById('chat-input')
  const text = input.value.trim()
  if ((!text && pendingImages.length === 0 && pendingFiles.length === 0) || isStreaming) return

  appendUserMessage(text, pendingImages, pendingFiles)
  input.value = ''
  autoResizeTextarea(input)

  const images = pendingImages.slice()
  const files  = pendingFiles.slice()
  pendingImages = []
  pendingFiles  = []
  renderPreviews()

  setStreaming(true)
  send({ type: 'chat', content: text, images: images.length ? images : undefined, files: files.length ? files : undefined, disabledTools: getDisabledTools() })
}

window.sendChat = sendChat

window.handleChatKey = function(e) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    sendChat()
  }
  setTimeout(() => autoResizeTextarea(e.target), 0)
}

window.resetChat  = function() { send({ type: 'reset_chat' }); pendingImages = []; pendingFiles = []; pendingAttachments = []; renderPreviews() }

window.cancelChat = function() {
  send({ type: 'cancel' })
  setStreaming(false)
  finalizeStream()
}

function autoResizeTextarea(el) {
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 180) + 'px'
}

function setStreaming(v) {
  isStreaming = v
  const btn = document.getElementById('send-btn')
  if (v) {
    btn.classList.add('loading')
    btn.innerHTML = '◼'
    btn.onclick = cancelChat
  } else {
    btn.classList.remove('loading')
    btn.innerHTML = '<svg width="15" height="15" viewBox="0 0 16 16" fill="none"><path d="M1 8L15 1L8 15L7 9L1 8Z" fill="currentColor"/></svg>'
    btn.onclick = sendChat
  }
}

function fmtTime(date) {
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function appendUserMessage(text, images, files) {
  const msgs = document.getElementById('chat-messages')
  const div = document.createElement('div')
  div.className = 'chat-msg user'
  let imagesHtml = ''
  if (images && images.length > 0) {
    imagesHtml = '<div class="chat-msg-images">' +
      images.map(b64 => `<img src="data:image/png;base64,${b64}" alt="image">`).join('') +
      '</div>'
  }
  let filesHtml = ''
  if (files && files.length > 0) {
    filesHtml = '<div class="chat-msg-files">' +
      files.map(f => `<span class="chat-msg-file-chip" title="${escHtml(f.name)}">📄 ${escHtml(f.name)}</span>`).join('') +
      '</div>'
  }
  const timeStr = fmtTime(new Date())
  div.innerHTML = `<div class="chat-msg-role">You <span class="chat-msg-time">${timeStr}</span></div>${imagesHtml}${filesHtml}<div class="chat-msg-content">${escHtml(text)}</div>`
  msgs.appendChild(div)
  scrollChat()
}

function appendStream(content) {
  if (!currentAssistantEl) {
    const msgs = document.getElementById('chat-messages')
    const div = document.createElement('div')
    div.className = 'chat-msg assistant'
    const timeStr = fmtTime(new Date())
    div.innerHTML = `<div class="chat-msg-role">AI <span class="chat-msg-time">${timeStr}</span></div><div class="chat-msg-content cursor"></div>`
    msgs.appendChild(div)
    currentAssistantEl = div.querySelector('.chat-msg-content')
    currentAssistantContent = ''
  }
  currentAssistantContent += content
  currentAssistantEl.innerHTML = renderMarkdown(currentAssistantContent)
  currentAssistantEl.classList.add('cursor')
  scrollChat()
}

function finalizeStream() {
  if (currentAssistantEl) {
    currentAssistantEl.classList.remove('cursor')
    currentAssistantEl.innerHTML = renderMarkdown(currentAssistantContent)
    if (pendingAttachments.length > 0) {
      const assistantDiv = currentAssistantEl.closest('.chat-msg.assistant')
      if (assistantDiv) {
        const imgContainer = document.createElement('div')
        imgContainer.className = 'tool-block-images'
        pendingAttachments.forEach(b64 => {
          const img = document.createElement('img')
          img.src = 'data:image/jpeg;base64,' + b64
          img.alt = 'attachment'
          imgContainer.appendChild(img)
        })
        assistantDiv.appendChild(imgContainer)
      }
      pendingAttachments = []
    }
    currentAssistantEl = null
    currentAssistantContent = ''
  }
  setStreaming(false)
  scrollChat()
}

function appendToolUse(msg) {
  if (currentAssistantEl) {
    currentAssistantEl.classList.remove('cursor')
    currentAssistantEl = null
    currentAssistantContent = ''
  }

  const msgs = document.getElementById('chat-messages')
  const div = document.createElement('div')
  div.className = 'chat-msg tool'
  div.id = 'tool-' + msg.id

  let inputStr = ''
  try {
    const inp = typeof msg.input === 'string' ? JSON.parse(msg.input) : msg.input
    inputStr = formatToolInput(msg.tool, inp)
  } catch { inputStr = String(msg.input) }

  div.innerHTML = `
    <div class="tool-block">
      <div class="tool-block-header">
        <span class="tool-block-name">${escHtml(msg.tool)}</span>
        <span class="tool-block-input-inline">${escHtml(inputStr)}</span>
      </div>
      <div class="tool-block-output running" id="tool-out-${msg.id}"><span class="tool-spinner"></span> Running…</div>
    </div>`
  msgs.appendChild(div)
  scrollChat()
}

function appendToolResult(msg) {
  const el = document.getElementById('tool-out-' + msg.id)
  if (!el) {
    console.error('[prism] tool_result orphan — no element for id:', msg.id, msg)
    return
  }
  if (el) {
    el.classList.remove('running')
    el.textContent = msg.output || '(no output)'
    if (msg.output?.startsWith('ERROR')) el.classList.add('error')
    if (msg.images && msg.images.length > 0) {
      const toolBlock = el.closest('.tool-block')
      if (toolBlock) {
        const imgContainer = document.createElement('div')
        imgContainer.className = 'tool-block-images'
        msg.images.forEach(b64 => {
          const img = document.createElement('img')
          img.src = 'data:image/jpeg;base64,' + b64
          img.alt = 'page image'
          imgContainer.appendChild(img)
        })
        toolBlock.appendChild(imgContainer)
      }
    }
  }
  scrollChat()
}

function appendError(text) {
  const msgs = document.getElementById('chat-messages')
  const div = document.createElement('div')
  div.className = 'chat-msg assistant'
  div.innerHTML = `<div class="chat-msg-content" style="color:var(--red)">${escHtml(text)}</div>`
  msgs.appendChild(div)
  setStreaming(false)
  scrollChat()
}

function clearChat() {
  document.getElementById('chat-messages').innerHTML = ''
  currentAssistantEl = null
  currentAssistantContent = ''
  setStreaming(false)
}

function restoreChatHistory(messages) {
  if (!messages || messages.length === 0) return
  const msgs = document.getElementById('chat-messages')
  const sep = document.createElement('div')
  sep.className = 'chat-history-sep'
  sep.textContent = '— previous conversation —'
  msgs.appendChild(sep)
  for (const m of messages) {
    const timeStr = m.createdAt ? fmtTime(new Date(m.createdAt)) : ''
    const timeBadge = timeStr ? ` <span class="chat-msg-time">${timeStr}</span>` : ''
    if (m.role === 'user') {
      const div = document.createElement('div')
      div.className = 'chat-msg user'
      div.innerHTML = `<div class="chat-msg-role">You${timeBadge}</div><div class="chat-msg-content">${escHtml(m.content)}</div>`
      msgs.appendChild(div)
    } else if (m.role === 'assistant' && m.content) {
      const div = document.createElement('div')
      div.className = 'chat-msg assistant'
      div.innerHTML = `<div class="chat-msg-role">AI${timeBadge}</div><div class="chat-msg-content">${renderMarkdown(m.content)}</div>`
      msgs.appendChild(div)
    } else if (m.role === 'tool') {
      let inputStr = ''
      try {
        const inp = typeof m.toolInput === 'string' ? JSON.parse(m.toolInput) : m.toolInput
        inputStr = formatToolInput(m.toolName, inp)
      } catch { inputStr = '' }
      const isError = m.content && m.content.startsWith('ERROR')
      const div = document.createElement('div')
      div.className = 'chat-msg tool'
      div.innerHTML = `
        <div class="tool-block">
          <div class="tool-block-header">
            <span class="tool-block-name">${escHtml(m.toolName || 'tool')}</span>
            <span class="tool-block-input-inline">${escHtml(inputStr)}</span>
          </div>
          <div class="tool-block-output${isError ? ' error' : ''}">${escHtml(m.content || '(no output)')}</div>
        </div>`
      msgs.appendChild(div)
    }
  }
  const sep2 = document.createElement('div')
  sep2.className = 'chat-history-sep'
  sep2.textContent = '— now —'
  msgs.appendChild(sep2)
  scrollChat()
}

function scrollChat() {
  const msgs = document.getElementById('chat-messages')
  msgs.scrollTop = msgs.scrollHeight
}

function formatToolInput(tool, inp) {
  if (!inp) return ''
  switch (tool) {
    case 'exec_command':      return '$ ' + (inp.command || '')
    case 'write_file':        return `→ ${inp.path}`
    case 'read_file':         return `← ${inp.path}`
    case 'list_files':        return `ls ${inp.path || '.'}`
    case 'apt_install':       return `apt install ${inp.packages}`
    case 'pip_install':       return `pip install ${inp.packages}`
    case 'install_packages':  return `${inp.manager} install ${inp.packages}`
    case 'docker_manage':     return `docker ${inp.action}${inp.name ? ' ' + inp.name : ''}`
    case 'widget':            return inp.action === 'add' || inp.action === 'update' ? `${inp.action} "${inp.title || inp.id}"` : `${inp.action} widget${inp.id ? ': ' + inp.id : 's'}`
    case 'cron':              return inp.action === 'add' ? `${inp.schedule} → ${inp.name}` : `${inp.action}${inp.name ? ': ' + inp.name : ' jobs'}`
    case 'rag_manage':        return `rag ${inp.action}${inp.collection ? ': ' + inp.collection : ' collections'}`
    case 'secrets':           return `secrets ${inp.action}${inp.name ? ': ' + inp.name : ''}`
    case 'mcp':               return `⬡ ${inp.action}${inp.name ? ': ' + inp.name : ''}`
    case 'add_widget':        return `"${inp.title}" — cols=${inp.cols||1} height=${inp.height||280}px`
    case 'remove_widget':     return `remove widget: ${inp.id}`
    case 'http_request':      return `${inp.method||'GET'} ${inp.url}`
    case 'web_search':        return `🔍 ${inp.query}`
    case 'browser_get':       return `🌐 ${inp.url}`
    case 'notify':            return inp.delay_seconds > 0 ? `🔔 in ${inp.delay_seconds}s — ${inp.title}` : `🔔 ${inp.title}`
    case 'rag_list':          return inp.collection ? `rag: ${inp.collection}` : 'rag: list collections'
    case 'rag_search':        return `rag: "${inp.query}" in ${inp.collection}`
    case 'rag_ingest':        return `rag: ingest "${inp.source}" → ${inp.collection}`
    case 'cron_list':         return '(list jobs)'
    case 'cron_add':          return `${inp.schedule} → ${inp.name}`
    case 'cron_remove':          return `remove: ${inp.name}`
    case 'update_system_prompt': return `✏️ update personality`
    case 'mcp_add_server':    return `⬡ ${inp.name} — ${inp.url}`
    case 'mcp_remove_server': return `⬡ remove: ${inp.name}`
    case 'mcp_list_servers':  return '(list MCP servers)'
    default: return JSON.stringify(inp)
  }
}

// ─── Notifications ────────────────────────────────────────────────────────────

const NOTIF_ICONS = { info: 'ℹ', success: '✓', warning: '⚠', error: '✕' }
const NOTIF_COLORS = { info: 'var(--accent)', success: 'var(--green)', warning: 'var(--yellow)', error: 'var(--red)' }

function loadNotificationsHistory(notifs) {
  notifications = notifs || []
  renderNotifPanel()
  renderNotifBadge()
}

function receiveNotification(notif) {
  // Avoid duplicates
  if (notifications.find(n => n.id === notif.id)) return
  notifications.push(notif)
  renderNotifPanel()
  renderNotifBadge()
  showToast(notif)
  sendBrowserNotification(notif)
}

function renderNotifBadge() {
  const badge = document.getElementById('notif-badge')
  const btn   = document.getElementById('notif-btn')
  if (!badge) return
  const unread = notifications.filter(n => !n.read).length
  if (unread > 0) {
    badge.textContent = unread > 99 ? '99+' : unread
    badge.style.display = 'flex'
    btn.classList.add('has-notif')
  } else {
    badge.style.display = 'none'
    btn.classList.remove('has-notif')
  }
}

function renderNotifPanel() {
  const list = document.getElementById('notif-list')
  if (!list) return
  if (notifications.length === 0) {
    list.innerHTML = '<div class="notif-empty">No notifications</div>'
    return
  }
  list.innerHTML = ''
  // Show newest first
  const sorted = [...notifications].reverse()
  for (const n of sorted) {
    const el = document.createElement('div')
    el.className = 'notif-item' + (n.read ? ' read' : '')
    const color  = NOTIF_COLORS[n.level] || NOTIF_COLORS.info
    const icon   = NOTIF_ICONS[n.level]  || NOTIF_ICONS.info
    const timeStr = n.createdAt ? fmtTime(new Date(n.createdAt)) : ''
    el.innerHTML = `
      <span class="notif-icon" style="color:${color}">${icon}</span>
      <div class="notif-body">
        <div class="notif-title">${escHtml(n.title)}</div>
        ${n.message ? `<div class="notif-msg">${escHtml(n.message)}</div>` : ''}
      </div>
      <span class="notif-time">${timeStr}</span>
      <button class="notif-delete" onclick="deleteNotification(${n.id})" title="Delete">✕</button>`
    list.appendChild(el)
  }
}

window.markAllNotifRead = function() {
  send({ type: 'mark_notifications_read' })
}

window.deleteNotification = function(id) {
  send({ type: 'delete_notification', id: String(id) })
}

function markNotificationsReadLocal() {
  notifications.forEach(n => n.read = true)
  renderNotifPanel()
  renderNotifBadge()
}

function removeNotificationLocal(id) {
  notifications = notifications.filter(n => String(n.id) !== String(id))
  renderNotifPanel()
  renderNotifBadge()
}

window.toggleNotifPanel = function() {
  notifPanelOpen = !notifPanelOpen
  const panel = document.getElementById('notif-panel')
  if (!panel) return
  panel.style.display = notifPanelOpen ? 'flex' : 'none'
  if (notifPanelOpen) {
    markAllNotifRead()
    renderNotifPanel()
  }
}

// Toast notification
function showToast(notif) {
  const toast = document.createElement('div')
  toast.className = 'notif-toast notif-toast-' + (notif.level || 'info')
  const color = NOTIF_COLORS[notif.level] || NOTIF_COLORS.info
  const icon  = NOTIF_ICONS[notif.level]  || NOTIF_ICONS.info
  toast.innerHTML = `
    <span class="notif-toast-icon" style="color:${color}">${icon}</span>
    <div class="notif-toast-body">
      <div class="notif-toast-title">${escHtml(notif.title)}</div>
      ${notif.message ? `<div class="notif-toast-msg">${escHtml(notif.message)}</div>` : ''}
    </div>
    <button class="notif-toast-close">×</button>`
  toast.querySelector('.notif-toast-close').onclick = () => toast.remove()
  toast.onclick = (e) => { if (e.target.className !== 'notif-toast-close') { toast.remove(); if (!notifPanelOpen) toggleNotifPanel() } }
  document.getElementById('toast-container').appendChild(toast)
  setTimeout(() => toast.classList.add('visible'), 10)
  setTimeout(() => { toast.classList.remove('visible'); setTimeout(() => toast.remove(), 300) }, 6000)
}

// Browser notifications
function sendBrowserNotification(notif) {
  if (document.hasFocus()) return
  if (!('Notification' in window)) return
  if (Notification.permission === 'granted') {
    new Notification(notif.title, { body: notif.message || '', icon: '/favicon.ico' })
  } else if (Notification.permission !== 'denied') {
    Notification.requestPermission()
  }
}

// Close notif panel when clicking outside
document.addEventListener('click', e => {
  const panel  = document.getElementById('notif-panel')
  const btn    = document.getElementById('notif-btn')
  if (notifPanelOpen && !panel?.contains(e.target) && e.target !== btn && !btn?.contains(e.target)) {
    notifPanelOpen = false
    if (panel) panel.style.display = 'none'
  }
})

// ─── Secret dialog ────────────────────────────────────────────────────────────

function showSecretDialog(name, description) {
  const overlay = document.getElementById('secret-overlay')
  const dialog  = document.getElementById('secret-dialog')
  const desc    = document.getElementById('secret-dialog-desc')
  const input   = document.getElementById('secret-dialog-input')
  desc.textContent = description || `Valeur pour "${name}"`
  input.value = ''
  overlay.classList.add('visible')
  dialog.classList.add('visible')
  setTimeout(() => input.focus(), 80)
}

function closeSecretDialog() {
  document.getElementById('secret-overlay').classList.remove('visible')
  document.getElementById('secret-dialog').classList.remove('visible')
  document.getElementById('secret-dialog-input').value = ''
}

window.cancelSecretDialog = function() {
  closeSecretDialog()
  send({ type: 'secret_response', content: '' })
}

window.submitSecretDialog = function() {
  const val = document.getElementById('secret-dialog-input').value
  if (!val) return
  closeSecretDialog()
  send({ type: 'secret_response', content: val })
}

window.handleSecretKey = function(e) {
  if (e.key === 'Enter') { e.preventDefault(); submitSecretDialog() }
  if (e.key === 'Escape') { e.preventDefault(); cancelSecretDialog() }
}

// ─── Sessions ─────────────────────────────────────────────────────────────────

async function loadSessions() {
  try {
    const res = await fetch('/api/sessions')
    const data = await res.json()
    renderSessionSwitcher(data.sessions || [])
  } catch {}
}

function renderSessionSwitcher(sessions) {
  const label = document.getElementById('session-label')
  const list  = document.getElementById('session-list')
  if (!label || !list) return

  const current = sessions.find(s => s.id === currentSessionID)
  label.textContent = current ? current.name : currentSessionID

  list.innerHTML = ''
  for (const sess of sessions) {
    const row = document.createElement('div')
    row.className = 'session-row' + (sess.id === currentSessionID ? ' active' : '')

    const nameBtn = document.createElement('button')
    nameBtn.className = 'session-name-btn'
    nameBtn.textContent = sess.name
    nameBtn.onclick = () => { closeSessionMenu(); if (sess.id !== currentSessionID) switchToSession(sess.id) }
    row.appendChild(nameBtn)

    const rename = document.createElement('button')
    rename.className = 'session-del-btn'
    rename.title = 'Rename'
    rename.textContent = '✎'
    rename.onclick = async (e) => {
      e.stopPropagation()
      const newName = prompt('Rename board:', sess.name)
      if (!newName || newName.trim() === sess.name) return
      await fetch(`/api/sessions/${sess.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: newName.trim() })
      })
      loadSessions()
    }
    row.appendChild(rename)

    if (sess.id !== currentSessionID) {
      const del = document.createElement('button')
      del.className = 'session-del-btn'
      del.title = 'Delete'
      del.textContent = '×'
      del.onclick = async (e) => {
        e.stopPropagation()
        if (!confirm(`Delete board "${sess.name}" and all its history?`)) return
        await fetch(`/api/sessions/${sess.id}`, { method: 'DELETE' })
        loadSessions()
      }
      row.appendChild(del)
    }
    list.appendChild(row)
  }
}

function switchToSession(id) {
  batchLoading = true
  clearChat()
  for (const wid of [...widgets.keys()]) removeWidget(wid)
  currentSessionID = id
  localStorage.setItem('active-session', id)
  updateSettingsLink()
  pendingImages = []
  pendingFiles  = []
  renderPreviews()
  notifications = []
  renderNotifPanel()
  renderNotifBadge()
  if (ws) { ws.onclose = null; ws.close() }
  connect()
}

async function promptNewSession() {
  closeSessionMenu()
  const name = prompt('New board name:')
  if (!name || !name.trim()) return
  try {
    const res = await fetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name.trim() })
    })
    const data = await res.json()
    switchToSession(data.id)
  } catch (e) {
    alert('Failed to create: ' + e.message)
  }
}

window.toggleSessionMenu = function() {
  sessionMenuOpen = !sessionMenuOpen
  const menu = document.getElementById('session-menu')
  if (menu) menu.style.display = sessionMenuOpen ? 'block' : 'none'
  if (sessionMenuOpen) loadSessions()
}

function closeSessionMenu() {
  sessionMenuOpen = false
  const menu = document.getElementById('session-menu')
  if (menu) menu.style.display = 'none'
}

window.promptNewSession = promptNewSession

// Close session menu when clicking outside
document.addEventListener('click', e => {
  if (sessionMenuOpen && !document.getElementById('session-switcher')?.contains(e.target)) {
    closeSessionMenu()
  }
})

// ─── File / Image upload ──────────────────────────────────────────────────────

// Router: images go into pendingImages, text/doc files are uploaded for parsing.
function addAttachment(file) {
  if (!file) return
  if (file.type.startsWith('image/')) {
    addImageFromFile(file)
  } else {
    addFileAttachment(file)
  }
}
window.addAttachment = addAttachment

function addImageFromFile(file) {
  if (!file || !file.type.startsWith('image/')) return
  const reader = new FileReader()
  reader.onload = e => {
    const b64 = e.target.result.split(',')[1]
    pendingImages.push(b64)
    renderPreviews()
  }
  reader.readAsDataURL(file)
}

async function addFileAttachment(file) {
  const form = new FormData()
  form.append('file', file)
  try {
    const res = await fetch('/api/chat/upload', { method: 'POST', body: form })
    if (!res.ok) {
      const msg = await res.text()
      alert(`Could not attach "${file.name}": ${msg}`)
      return
    }
    const { name, text } = await res.json()
    pendingFiles.push({ name, text })
    renderPreviews()
  } catch (err) {
    alert(`Could not attach "${file.name}": ${err}`)
  }
}

function renderPreviews() {
  const container = document.getElementById('image-previews')
  if (!container) return
  container.innerHTML = ''
  if (pendingImages.length === 0 && pendingFiles.length === 0) {
    container.style.display = 'none'
    return
  }
  container.style.display = 'flex'
  pendingImages.forEach((b64, i) => {
    const wrap = document.createElement('div')
    wrap.className = 'img-preview-wrap'
    const img = document.createElement('img')
    img.src = 'data:image/png;base64,' + b64
    const btn = document.createElement('button')
    btn.className = 'img-preview-remove'
    btn.textContent = '×'
    btn.title = 'Remove'
    btn.addEventListener('click', () => { pendingImages.splice(i, 1); renderPreviews() })
    wrap.appendChild(img)
    wrap.appendChild(btn)
    container.appendChild(wrap)
  })
  pendingFiles.forEach((f, i) => {
    const chip = document.createElement('div')
    chip.className = 'file-preview-chip'
    chip.title = f.name
    chip.innerHTML = `<span class="file-preview-name">📄 ${escHtml(f.name)}</span>`
    const btn = document.createElement('button')
    btn.className = 'img-preview-remove'
    btn.textContent = '×'
    btn.title = 'Remove'
    btn.addEventListener('click', () => { pendingFiles.splice(i, 1); renderPreviews() })
    chip.appendChild(btn)
    container.appendChild(chip)
  })
}

// Paste images from clipboard (e.g. screenshot)
document.addEventListener('paste', e => {
  if (!chatOpen) return
  const items = e.clipboardData?.items
  if (!items) return
  for (const item of items) {
    if (item.type.startsWith('image/')) {
      addImageFromFile(item.getAsFile())
      e.preventDefault()
    }
  }
})

// ─── Shared config helpers (read by app.js when sending chat messages) ────────

const CONFIG_KEY = 'agent-config'
function getConfig() {
  try { return JSON.parse(localStorage.getItem(CONFIG_KEY) || '{}') } catch { return {} }
}
function getDisabledTools() { return getConfig().disabled || [] }

// ─── Model selector ───────────────────────────────────────────────────────────

window.setModel = function(model) { send({ type: 'set_model', model }) }

function setCurrentModel(model) {
  const sel = document.getElementById('model-select')
  let found = false
  for (const opt of sel.options) { if (opt.value === model) { found = true; break } }
  if (!found) {
    const opt = document.createElement('option')
    opt.value = model; opt.textContent = model
    sel.appendChild(opt)
  }
  sel.value = model
}

async function loadModels() {
  try {
    const resp = await fetch('/api/models')
    const data = await resp.json()
    const sel = document.getElementById('model-select')
    const current = sel.value
    sel.innerHTML = ''
    for (const m of (data.models || [])) {
      const opt = document.createElement('option')
      opt.value = m; opt.textContent = m
      sel.appendChild(opt)
    }
    if (current) sel.value = current
  } catch {}
}

// ─── Status badge ─────────────────────────────────────────────────────────────

function setContainerBadge(status) {
  const badge = document.getElementById('container-badge')
  badge.className = 'badge'
  const map = {
    running:     ['badge-running', 'running'],
    exited:      ['badge-stopped', 'stopped'],
    'not found': ['badge-unknown', 'not found'],
    unavailable: ['badge-unknown', 'unavailable'],
  }
  const [cls, label] = map[status] || ['badge-unknown', status]
  badge.classList.add(cls)
  badge.title = 'Container: ' + label
}

// ─── Markdown ─────────────────────────────────────────────────────────────────

function renderMarkdown(text) {
  if (!text) return ''

  let prefixHtml = ''

  // Complete thinking/thought blocks → collapsible details
  text = text.replace(/<th(?:inking|ought|ink)>([\s\S]*?)<\/th(?:inking|ought|ink)>/gi, (_, content) => {
    const trimmed = content.trim()
    if (!trimmed) return ''
    prefixHtml += `<details class="thinking-block"><summary><span class="thinking-icon">💭</span> Reasoning</summary><div class="thinking-content">${escHtml(trimmed).replace(/\n/g, '<br>')}</div></details>`
    return ''
  })

  // Incomplete thinking block during streaming → animated indicator
  let activelyThinking = false
  if (/<th(?:inking|ought|ink)>/i.test(text)) {
    text = text.replace(/<th(?:inking|ought|ink)>[\s\S]*$/i, '')
    activelyThinking = true
  }

  text = text.trim()

  if (!text && !prefixHtml && !activelyThinking) return ''

  let result = prefixHtml
  if (activelyThinking) {
    result += '<div class="thinking-indicator"><span class="thinking-dots"></span>Thinking…</div>'
  }

  if (!text) return result

  // Extract math blocks before marked so it doesn't mangle underscores/stars in LaTeX
  const mathBlocks = []
  const placeholder = (i) => `\x00MATH${i}\x00`
  text = text.replace(/\$\$([\s\S]*?)\$\$/g, (_, inner) => {
    mathBlocks.push({ inner, display: true })
    return placeholder(mathBlocks.length - 1)
  })
  text = text.replace(/\$([^\$\n]+?)\$/g, (_, inner) => {
    mathBlocks.push({ inner, display: false })
    return placeholder(mathBlocks.length - 1)
  })

  let html = marked.parse(text, { breaks: true, gfm: true })

  // Restore math blocks as KaTeX-rendered HTML
  html = html.replace(/\x00MATH(\d+)\x00/g, (_, i) => {
    const { inner, display } = mathBlocks[parseInt(i)]
    try {
      return katex.renderToString(inner, { displayMode: display, throwOnError: false, output: 'html' })
    } catch { return display ? `$$${inner}$$` : `$${inner}$` }
  })

  result += html
  return result
}

function escHtml(str) {
  if (!str) return ''
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

// ─── Emoji picker ─────────────────────────────────────────────────────────────

const EMOJI_CATEGORIES = [
  { icon: '😊', name: 'Smileys',    emojis: ['😊','😂','😍','🥰','😎','🤔','😅','🤣','😭','😤','🥲','😏','🤩','😴','🤯','🙄','😬','🤗','😇','🥳','😆','😋','🤭','🫠','😮','😱','😰','😓','🥺','😜'] },
  { icon: '👍', name: 'Gestures',  emojis: ['👍','👎','👏','🙌','🤝','💪','✌️','🤞','🫡','🫶','❤️','💯','🔥','✨','🎉','🎊','👀','💡','💥','🌟','⚡','🏆','🚀','💬','📌'] },
  { icon: '🐶', name: 'Animals',   emojis: ['🐶','🐱','🐭','🐹','🐼','🦊','🐻','🐨','🦁','🐯','🐸','🐧','🐦','🦆','🦅','🦉','🦋','🐝','🦀','🐬','🐳','🦓','🐙','🦑','🦜'] },
  { icon: '🍕', name: 'Food',      emojis: ['🍕','🍔','🍟','🌮','🍜','🍣','🍱','🍦','🎂','🍩','🍪','🍫','☕','🧃','🍺','🥂','🍷','🧁','🍳','🥑','🥐','🍉','🍓','🫐','🥝'] },
  { icon: '💻', name: 'Objects',   emojis: ['💻','📱','🎮','📷','🎵','🎧','📚','📝','💰','🔑','🎯','🌈','⭐','🌙','☀️','🌊','🏔️','🌸','🎸','🛸','🏠','⌚','🔭','🧲','🎲'] },
  { icon: '✅', name: 'Symbols',   emojis: ['✅','❌','⚠️','ℹ️','🔴','🟠','🟡','🟢','🔵','🟣','🔶','🔷','💠','▶️','⏸️','⏹️','🔁','🔀','📌','🏷️','🔖','🔔','❓','❗','➕'] },
]

let emojiPickerEl = null
let emojiPickerOpen = false

function buildEmojiPicker() {
  if (emojiPickerEl) return

  const picker = document.createElement('div')
  picker.className = 'emoji-picker'

  const tabs = document.createElement('div')
  tabs.className = 'emoji-tabs'

  const grid = document.createElement('div')
  grid.className = 'emoji-grid'

  EMOJI_CATEGORIES.forEach((cat, i) => {
    const tab = document.createElement('button')
    tab.className = 'emoji-tab' + (i === 0 ? ' active' : '')
    tab.title = cat.name
    tab.textContent = cat.icon
    tab.onclick = () => {
      tabs.querySelectorAll('.emoji-tab').forEach(t => t.classList.remove('active'))
      tab.classList.add('active')
      fillEmojiGrid(grid, cat.emojis)
    }
    tabs.appendChild(tab)
  })

  fillEmojiGrid(grid, EMOJI_CATEGORIES[0].emojis)
  picker.appendChild(tabs)
  picker.appendChild(grid)

  document.addEventListener('click', e => {
    if (emojiPickerOpen && !picker.contains(e.target) && e.target.id !== 'emoji-btn') {
      picker.style.display = 'none'
      emojiPickerOpen = false
    }
  })

  document.getElementById('chat-input-area').appendChild(picker)
  emojiPickerEl = picker
}

function fillEmojiGrid(grid, emojis) {
  grid.innerHTML = ''
  emojis.forEach(emoji => {
    const btn = document.createElement('button')
    btn.className = 'emoji-item'
    btn.textContent = emoji
    btn.onclick = () => insertEmoji(emoji)
    grid.appendChild(btn)
  })
}

window.toggleEmojiPicker = function() {
  buildEmojiPicker()
  emojiPickerOpen = !emojiPickerOpen
  emojiPickerEl.style.display = emojiPickerOpen ? 'flex' : 'none'
}

function insertEmoji(emoji) {
  const ta = document.getElementById('chat-input')
  const start = ta.selectionStart
  const end = ta.selectionEnd
  ta.value = ta.value.slice(0, start) + emoji + ta.value.slice(end)
  ta.selectionStart = ta.selectionEnd = start + emoji.length
  ta.focus()
  ta.dispatchEvent(new Event('input'))
}

// ─── File editor ──────────────────────────────────────────────────────────────

let editorPath = null
let cmEditor = null

const EXT_MODE = {
  js: 'javascript', mjs: 'javascript', cjs: 'javascript',
  ts: { name: 'javascript', typescript: true },
  jsx: { name: 'javascript', jsx: true },
  tsx: { name: 'javascript', typescript: true, jsx: true },
  json: { name: 'javascript', json: true },
  py: 'python',
  go: 'go',
  css: 'css',
  html: 'htmlmixed', htm: 'htmlmixed',
  xml: 'xml', svg: 'xml',
  md: 'markdown', markdown: 'markdown',
  sh: 'shell', bash: 'shell', zsh: 'shell',
  yaml: 'yaml', yml: 'yaml',
  toml: 'toml',
}

function editorMode(path) {
  const ext = (path.split('.').pop() || '').toLowerCase()
  return EXT_MODE[ext] || null
}

function initCodeMirror() {
  if (cmEditor) return
  cmEditor = CodeMirror(document.getElementById('editor-codemirror'), {
    theme: 'dracula',
    lineNumbers: true,
    indentUnit: 2,
    tabSize: 2,
    indentWithTabs: false,
    lineWrapping: false,
    autofocus: false,
    extraKeys: {
      'Ctrl-S': () => editorSave(),
      'Cmd-S':  () => editorSave(),
      'Escape': () => closeEditor(),
      Tab: cm => {
        if (cm.somethingSelected()) cm.indentSelection('add')
        else cm.replaceSelection('  ', 'end')
      },
    },
  })
}

function openEditor(path, content) {
  editorPath = path
  document.getElementById('editor-filename').textContent = path
  document.getElementById('editor-status').textContent = ''
  document.getElementById('editor-overlay').classList.add('visible')
  document.getElementById('editor-panel').classList.add('open')

  initCodeMirror()
  cmEditor.setOption('mode', editorMode(path))
  cmEditor.setValue(content)
  cmEditor.clearHistory()
  setTimeout(() => { cmEditor.refresh(); cmEditor.focus() }, 50)
}

window.closeEditor = function() {
  editorPath = null
  document.getElementById('editor-overlay').classList.remove('visible')
  document.getElementById('editor-panel').classList.remove('open')
}

window.editorSave = function() {
  if (!editorPath || !cmEditor) return
  send({ type: 'file_save', path: editorPath, data: { content: cmEditor.getValue() } })
}

function editorOnSaved(path) {
  if (editorPath !== path) return
  const el = document.getElementById('editor-status')
  el.textContent = '✓ Saved'
  setTimeout(() => { el.textContent = '' }, 2500)
}

// Listen for postMessage calls from widget iframes (Dashboard API)
window.addEventListener('message', e => {
  const d = e.data
  if (!d || !d.type) return
  if (d.type === 'openFile' && d.path) {
    send({ type: 'file_open', path: d.path.replace(/^\/workspace\//, '') })
  } else if (d.type === 'sendChat' && d.text) {
    document.getElementById('chat-input').value = d.text
    if (!chatOpen) toggleChat()
    sendChat()
  } else if (d.type === 'notify' && d.message) {
    fetch('/api/notify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        session: currentSessionID,
        title: d.title || 'Widget',
        message: d.message,
        level: d.level || 'info'
      })
    }).then(r => r.json()).then(data => {
      receiveNotification({
        id: data.id,
        title: d.title || 'Widget',
        message: d.message,
        level: d.level || 'info',
        read: false,
        createdAt: new Date().toISOString()
      })
    }).catch(() => {
      showToast({ title: d.title || 'Widget', message: d.message, level: d.level || 'info' })
    })
  }
})

// ─── Chat drawer resize ────────────────────────────────────────────────────────

const DRAWER_WIDTH_KEY = 'chat-drawer-width'
const chatDrawer = document.getElementById('chat-drawer')

// Restore saved width
const savedDrawerWidth = localStorage.getItem(DRAWER_WIDTH_KEY)
if (savedDrawerWidth) chatDrawer.style.width = savedDrawerWidth

{
  const handle = document.getElementById('chat-resize-handle')
  let resizing = false, startX = 0, startWidth = 0

  handle.addEventListener('mousedown', e => {
    resizing = true
    startX = e.clientX
    startWidth = chatDrawer.offsetWidth
    handle.classList.add('dragging')
    document.body.style.cursor = 'ew-resize'
    document.body.style.userSelect = 'none'
    e.preventDefault()
  })

  document.addEventListener('mousemove', e => {
    if (!resizing) return
    const newWidth = Math.min(Math.max(startWidth + (startX - e.clientX), 280), window.innerWidth * 0.9)
    chatDrawer.style.width = newWidth + 'px'
  })

  document.addEventListener('mouseup', () => {
    if (!resizing) return
    resizing = false
    handle.classList.remove('dragging')
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    localStorage.setItem(DRAWER_WIDTH_KEY, chatDrawer.style.width)
  })
}


// ─── Auth ─────────────────────────────────────────────────────────────────────

async function submitLogin() {
  const input = document.getElementById('login-input')
  const err   = document.getElementById('login-error')
  const btn   = document.getElementById('login-btn')
  err.style.display = 'none'
  btn.disabled = true
  btn.textContent = '…'
  try {
    const res = await fetch('/api/auth', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: input.value })
    })
    if (res.ok) {
      location.reload()
    } else {
      err.style.display = 'block'
      input.value = ''
      input.focus()
    }
  } finally {
    btn.disabled = false
    btn.textContent = 'Sign in'
  }
}

window.submitLogin = submitLogin

// ─── Init ─────────────────────────────────────────────────────────────────────

async function initApp() {
  const res  = await fetch('/api/auth')
  const data = await res.json()
  if (!data.authenticated) {
    document.getElementById('login-overlay').style.display = 'flex'
    document.getElementById('login-input').focus()
    return
  }

  initGrid()
  requestAnimationFrame(() => window.dispatchEvent(new Event('resize')))
  updateSettingsLink()
  connect()
  loadModels()
  setInterval(loadModels, 30000)

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      if (editorPath) closeEditor()
      else if (notifPanelOpen) toggleNotifPanel()
      else if (chatOpen) toggleChat()
    }
  })
}

initApp()
