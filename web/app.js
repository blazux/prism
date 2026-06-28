// Dashboard — Frontend

// ─── State ───────────────────────────────────────────────────────────────────

let ws = null
let isStreaming = false
let chatOpen = false
let currentAssistantEl = null
let currentAssistantContent = ''
const widgets = new Map()
let pendingImages = []      // base64 strings (without data-URL prefix) waiting to be sent
let pendingAttachments = [] // images queued by add_attachment, injected into next assistant bubble
let pendingFiles  = [] // {name, text} parsed file attachments waiting to be sent
// The workspace whose dashboard we return to. The live chat session
// (currentSessionID) follows the view: a workspace id on a dashboard, or the
// global ASSISTANT session while viewing a global app.
let lastWorkspace = localStorage.getItem('active-workspace') || localStorage.getItem('active-session') || 'default'
let currentSessionID = lastWorkspace

function updateSettingsLink() {
  const link = document.getElementById('settings-link')
  if (link) link.href = `/settings.html?session=${encodeURIComponent(currentSessionID)}#tools`
}
let batchLoading = false
let batchLoadingTimer = null
// Window geometry/open state is persisted server-side (set_plugin_state); the
// frontend keeps no localStorage layout. Kept here only as a no-op anchor.
let notifications = []   // [{id, title, message, level, read, createdAt}]
let notifPanelOpen = false

// ─── Dashboard windows ──────────────────────────────────────────────────────
// Widgets are free-floating windows (see windows.js). Their geometry and
// open/closed state live server-side in each widget's meta.json — persisted via
// set_plugin_state — so a board looks the same across reloads and devices.

const dashboard = () => document.getElementById('dashboard')
let cascadeN = 0

// Stagger new windows so they never stack exactly on top of each other.
function nextCascade(w, h) {
  const step = 28, base = 24
  const c = dashboard()
  const maxX = Math.max(base, c.clientWidth - w - base)
  const maxY = Math.max(base, c.clientHeight - h - base)
  const x = Math.min(base + cascadeN * step, maxX)
  const y = Math.min(base + cascadeN * step, maxY)
  cascadeN = (cascadeN + 1) % 12
  return { x, y }
}

function persistState(id, patch) {
  send({ type: 'set_plugin_state', id, ...patch })
}

// ─── WebSocket ────────────────────────────────────────────────────────────────

function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/ws?session=${encodeURIComponent(currentSessionID)}`)

  ws.onopen  = () => { clearChat(); batchLoading = true; if (currentContext) send({ type: 'set_context', content: currentContext }) }
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

// When the agent uses a PIM tool, refresh the matching app iframe if it's open
// so changes the agent makes via chat show up without a manual reload.
const PIM_TOOL_APP = { calendar: 'calendar', event: 'calendar', events: 'calendar', note: 'notes', notes: 'notes', task: 'tasks', tasks: 'tasks', cron: 'tasks' }
const pendingPimTools = {}
function trackPimTool(msg) { const app = PIM_TOOL_APP[msg.tool]; if (app) pendingPimTools[msg.id] = app }
function maybeRefreshApp(msg) {
  const app = pendingPimTools[msg.id]
  if (!app) return
  delete pendingPimTools[msg.id]
  if (currentView?.type === 'app' && currentView.name === app) {
    document.getElementById('app-frame')?.contentWindow?.postMessage({ type: 'data-changed', app }, '*')
  }
}

// ─── Server messages ──────────────────────────────────────────────────────────

function handleServerMsg(msg) {
  switch (msg.type) {
    case 'stream':          appendStream(msg.content); break
    case 'stream_end':      finalizeStream(); break
    case 'attachment':      pendingAttachments.push(...(msg.images || [])); break
    case 'tool_use':        appendToolUse(msg); trackPimTool(msg); break
    case 'tool_result':     appendToolResult(msg); maybeRefreshApp(msg); break
    case 'progress':        appendProgress(msg.content); break
    case 'plugin_load':
      addWidget(msg)
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

const WIDGET_SANDBOX = 'allow-scripts allow-same-origin allow-forms allow-popups allow-downloads allow-modals allow-popups-to-escape-sandbox'

// addWidget handles a plugin_load: create (or update) the widget record from
// its persisted meta, then mount it as a window if it is open. `meta` carries
// id, title, content, cols, height, locked, open and the saved x/y/w/h.
function addWidget(meta) {
  const id = meta.id
  const prev = widgets.get(id)

  // Tear down any existing instance (update flow).
  if (prev?.win) prev.win.destroy()
  if (prev?.el) prev.el.remove()

  const cols = Math.max(1, Math.min(3, meta.cols || 1))
  const defW = cols === 1 ? 340 : cols === 2 ? 500 : 700
  const defH = (meta.height > 0 ? meta.height : 260) + 26  // + header/border

  // Open/closed and geometry: explicit meta wins, else keep the previous
  // instance's, else fall back to defaults / cascade. Treat 0 as "unset".
  const open = (meta.open === undefined) ? (prev ? prev.open : true) : (meta.open !== false)
  let w = +meta.w || prev?.w || defW
  let h = +meta.h || prev?.h || defH
  let x = +meta.x || prev?.x || 0
  let y = +meta.y || prev?.y || 0
  if (!x && !y) { const c = nextCascade(w, h); x = c.x; y = c.y }

  const rec = {
    id, title: meta.title || id, content: meta.content || '',
    cols, height: meta.height || 280, locked: !!meta.locked,
    open, x, y, w, h, el: null, win: null,
  }
  widgets.set(id, rec)
  if (open) mountWindow(rec)
  renderDock()
  updateEmptyState()

  // A plugin_load with no open/geometry comes from a fresh agent add/update
  // (the server callback). Persist the chosen window placement so it survives a
  // reload. Server-driven restores always carry `open`, so they never re-save.
  if (meta.open === undefined && open) {
    persistState(id, { open: true, x: rec.x, y: rec.y, w: rec.w, h: rec.h })
  }
}

// mountWindow builds the window DOM and wires drag/resize for an open widget.
function mountWindow(rec) {
  const el = document.createElement('div')
  el.className = 'widget-window'
  el.id = 'widget-' + rec.id
  el.style.left = rec.x + 'px'
  el.style.top = rec.y + 'px'
  el.style.width = rec.w + 'px'
  el.style.height = rec.h + 'px'

  const card = document.createElement('div')
  card.className = 'widget-card'

  const hdr = document.createElement('div')
  hdr.className = 'widget-header'

  const titleEl = document.createElement('span')
  titleEl.className = 'widget-title'
  titleEl.textContent = rec.title

  const minBtn = document.createElement('button')
  minBtn.className = 'widget-min'
  minBtn.textContent = '–'
  minBtn.title = 'Minimize (keep in dock)'
  minBtn.addEventListener('click', () => minimizeWidget(rec.id))

  hdr.append(titleEl, minBtn)

  const body = document.createElement('div')
  body.className = 'widget-body'

  const iframe = document.createElement('iframe')
  iframe.srcdoc = window.PrismTheme.composeWidgetDoc(rec.content)
  iframe.setAttribute('sandbox', WIDGET_SANDBOX)
  body.appendChild(iframe)

  card.append(hdr, body)
  el.appendChild(card)
  dashboard().appendChild(el)

  rec.el = el
  rec.win = window.PrismWindows.makeWindow(el, {
    handle: hdr,
    container: dashboard(),
    onChange: (g) => {
      rec.x = g.x; rec.y = g.y; rec.w = g.w; rec.h = g.h
      persistState(rec.id, g)
    },
  })
}

// Minimize: hide the window but keep the widget (file stays on disk).
function minimizeWidget(id) {
  const rec = widgets.get(id)
  if (!rec) return
  if (rec.win) { rec.win.destroy(); rec.win = null }
  if (rec.el) { rec.el.remove(); rec.el = null }
  rec.open = false
  persistState(id, { open: false })
  renderDock()
  updateEmptyState()
}

// Restore a minimized widget back into an open window.
function restoreWidget(id) {
  const rec = widgets.get(id)
  if (!rec || rec.open) return
  rec.open = true
  if (!rec.x && !rec.y) { const c = nextCascade(rec.w, rec.h); rec.x = c.x; rec.y = c.y }
  mountWindow(rec)
  persistState(id, { open: true, x: rec.x, y: rec.y, w: rec.w, h: rec.h })
  renderDock()
  updateEmptyState()
}

// Permanently delete a widget (removes the files server-side, after confirm).
function deleteWidget(id) {
  const rec = widgets.get(id)
  if (rec?.locked) { showToast({ title: 'Widget locked', message: 'Unlock it before deleting.', level: 'warning' }); return }
  const title = rec?.title || id
  if (!confirm(`Delete widget “${title}” permanently? This cannot be undone.`)) return
  send({ type: 'remove_plugin', id })
}

// removeWidget handles a plugin_unload (server confirmed the widget is gone).
function removeWidget(id) {
  const rec = widgets.get(id)
  if (rec?.win) rec.win.destroy()
  if (rec?.el) rec.el.remove()
  widgets.delete(id)
  renderDock()
  updateEmptyState()
}

// ─── Widget dock ────────────────────────────────────────────────────────────
// A taskbar of every widget on the board: open ones are highlighted, minimized
// ones dimmed. Click toggles minimize/restore; the trash button deletes for good.
function renderDock() {
  const dock = document.getElementById('widget-dock')
  if (!dock) return
  dock.innerHTML = ''
  if (widgets.size === 0) { dock.style.display = 'none'; return }
  dock.style.display = 'flex'

  for (const rec of widgets.values()) {
    const chip = document.createElement('div')
    chip.className = 'dock-chip' + (rec.open ? ' open' : '')

    const btn = document.createElement('button')
    btn.className = 'dock-chip-label'
    btn.title = rec.open ? 'Minimize' : 'Reopen'
    btn.textContent = rec.title
    btn.addEventListener('click', () => rec.open ? minimizeWidget(rec.id) : restoreWidget(rec.id))

    const del = document.createElement('button')
    del.className = 'dock-chip-del'
    del.textContent = '🗑'
    del.title = 'Delete permanently'
    del.addEventListener('click', (e) => { e.stopPropagation(); deleteWidget(rec.id) })

    chip.append(btn, del)
    dock.appendChild(chip)
  }
}

function updateEmptyState() {
  const anyOpen = [...widgets.values()].some(w => w.open)
  document.getElementById('empty-state').style.display = anyOpen ? 'none' : 'flex'
}

// ─── Chat drawer ──────────────────────────────────────────────────────────────

window.toggleChat = function() {
  chatOpen = !chatOpen
  document.getElementById('chat-drawer').classList.toggle('open', chatOpen)
  document.getElementById('chat-fab').classList.toggle('active', chatOpen)
  if (chatOpen) setTimeout(() => document.getElementById('chat-input').focus(), 50)
}

// ─── Context-aware chat ─────────────────────────────────────────────────────────
// Tells the agent what the user is currently looking at (workspace, app, the
// open email/note…) so "summarize this" / "reply to it" resolve on their own.
let currentContext = ''
function setContext(text) {
  currentContext = text || ''
  const bar = document.getElementById('chat-context-bar')
  if (bar) {
    bar.textContent = currentContext
    bar.style.display = currentContext ? '' : 'none'
  }
  send({ type: 'set_context', content: currentContext })
}

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

// Live tool progress (e.g. deep_research step-by-step) — a muted log line.
function appendProgress(text) {
  const msgs = document.getElementById('chat-messages')
  if (!msgs) return
  const div = document.createElement('div')
  div.className = 'chat-progress'
  div.textContent = text || ''
  msgs.appendChild(div)
  msgs.scrollTop = msgs.scrollHeight
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

// ─── View router (rail: apps + boards) ─────────────────────────────────────────

const APP_TITLES = { email: 'Email', notes: 'Notes', tasks: 'Tasks', calendar: 'Calendar' }
const ASSISTANT = 'assistant'            // reserved session: the global super-agent
let currentView = { type: 'board' }      // { type:'board', workspace } | { type:'app', name }
let allSessions = []

// gotoSession reconnects the chat/WS to a different session (workspace or the
// global assistant). It does NOT change the view — setView decides the session.
function gotoSession(id) {
  if (id === currentSessionID) return
  batchLoading = true
  clearChat()
  cascadeN = 0
  for (const wid of [...widgets.keys()]) removeWidget(wid)
  currentSessionID = id
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

// setChatAgentLabel shows who you're talking to in the chat header.
function setChatAgentLabel(text) {
  const el = document.getElementById('chat-agent')
  if (el) el.textContent = text
}

function boardName(id) {
  const s = allSessions.find(x => x.id === id)
  return s ? s.name : id
}

// setView switches the main pane between the board canvas (widgets) and a
// full-pane app iframe. Apps are the same HTML used by board widgets, just
// maximized — same REST data underneath.
function setView(view) {
  currentView = view
  const dash  = document.getElementById('dashboard')
  const dock  = document.getElementById('widget-dock')
  const frame = document.getElementById('app-frame')
  const title = document.getElementById('view-title')

  document.querySelectorAll('.rail-item').forEach(el => el.classList.remove('active'))

  if (view.type === 'app') {
    // Global apps → chat is the global assistant.
    gotoSession(ASSISTANT)
    frame.src = `/apps/${view.name}.html?session=${encodeURIComponent(ASSISTANT)}`
    frame.style.display = ''
    dash.style.display = 'none'
    dock.style.display = 'none'
    if (title) title.textContent = APP_TITLES[view.name] || view.name
    document.querySelector(`.rail-item[data-app="${view.name}"]`)?.classList.add('active')
    setChatAgentLabel('🌐 Global assistant')
    setContext(`Viewing the ${APP_TITLES[view.name] || view.name} app`)
  } else {
    // Workspace dashboard → chat is that workspace's agent.
    const ws_id = view.workspace || lastWorkspace
    lastWorkspace = ws_id
    localStorage.setItem('active-workspace', ws_id)
    gotoSession(ws_id)
    frame.style.display = 'none'
    frame.src = 'about:blank'
    dash.style.display = ''
    renderDock()
    if (title) title.textContent = boardName(ws_id)
    document.querySelector(`.rail-item[data-board-id="${CSS.escape(ws_id)}"]`)?.classList.add('active')
    setChatAgentLabel(boardName(ws_id))
    setContext(`Workspace "${boardName(ws_id)}" — its dashboard (${widgets.size} widget(s))`)
  }
}

async function loadSessions() {
  try {
    const res = await fetch('/api/sessions')
    const data = await res.json()
    allSessions = data.sessions || []
    renderBoardList(allSessions)
    // Names arrive async — refresh the title/agent label if on a workspace.
    if (currentView.type === 'board') {
      const t = document.getElementById('view-title')
      if (t) t.textContent = boardName(lastWorkspace)
      setChatAgentLabel(boardName(lastWorkspace))
    }
  } catch {}
}

// ─── Workspace icons (client-side) ──────────────────────────────────────────────
const WS_ICON_KEY = 'prism-workspace-icons'
function wsIcons() { try { return JSON.parse(localStorage.getItem(WS_ICON_KEY) || '{}') } catch { return {} } }
function getWorkspaceIcon(id) { return wsIcons()[id] || '' }
function setWorkspaceIcon(id, emoji) {
  const m = wsIcons()
  if (emoji) m[id] = emoji; else delete m[id]
  localStorage.setItem(WS_ICON_KEY, JSON.stringify(m))
}
const escAttr = s => String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')

// Line icons (Feather-style) matching the app's aesthetic — they inherit
// currentColor, so they take the theme accent when active.
const WS_ICONS = {
  layout: '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/>',
  home: '<path d="M3 11l9-8 9 8"/><path d="M5 10v10h14V10"/>',
  briefcase: '<rect x="2" y="7" width="20" height="14" rx="2"/><path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16"/>',
  rocket: '<path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/><path d="M12 15l-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/>',
  code: '<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>',
  terminal: '<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>',
  chart: '<line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/>',
  activity: '<polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>',
  flask: '<path d="M9 3v6l-5 9a2 2 0 0 0 2 3h12a2 2 0 0 0 2-3l-5-9V3"/><path d="M8 3h8M7 14h10"/>',
  droplet: '<path d="M12 2.69l5.66 5.66a8 8 0 1 1-11.31 0z"/>',
  calendar: '<rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/>',
  bulb: '<path d="M9 18h6M10 22h4"/><path d="M12 2a7 7 0 0 0-4 12c1 1 1 2 1 3h6c0-1 0-2 1-3a7 7 0 0 0-4-12z"/>',
  globe: '<circle cx="12" cy="12" r="10"/><path d="M2 12h20M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20z"/>',
  folder: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
  music: '<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>',
  camera: '<path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/>',
  heart: '<path d="M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1-1.1a5.5 5.5 0 0 0-7.8 7.8l1 1L12 21l7.8-7.6 1-1a5.5 5.5 0 0 0 0-7.8z"/>',
  star: '<polygon points="12 2 15.1 8.6 22 9.3 17 14 18.2 21 12 17.6 5.8 21 7 14 2 9.3 8.9 8.6 12 2"/>',
  bookmark: '<path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/>',
  cpu: '<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3"/>',
  server: '<rect x="2" y="3" width="20" height="8" rx="2"/><rect x="2" y="13" width="20" height="8" rx="2"/><line x1="6" y1="7" x2="6.01" y2="7"/><line x1="6" y1="17" x2="6.01" y2="17"/>',
  database: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/><path d="M3 12a9 3 0 0 0 18 0"/>',
  cloud: '<path d="M18 10a4 4 0 0 0-7.7-1.5A4.5 4.5 0 1 0 6 18h12a3.5 3.5 0 0 0 0-7z"/>',
  mail: '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m2 7 10 6 10-6"/>',
  message: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
  users: '<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>',
  zap: '<polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>',
  coffee: '<path d="M18 8h1a4 4 0 0 1 0 8h-1"/><path d="M2 8h16v9a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4z"/><line x1="6" y1="1" x2="6" y2="4"/><line x1="10" y1="1" x2="10" y2="4"/><line x1="14" y1="1" x2="14" y2="4"/>',
  book: '<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5z"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
}
function wsIconSvg(name) {
  const p = WS_ICONS[name]
  if (!p) return ''
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${p}</svg>`
}

// editWorkspace opens a small dialog to rename a workspace AND pick its icon.
function editWorkspace(sess) {
  let selected = getWorkspaceIcon(sess.id)
  if (selected && !WS_ICONS[selected]) selected = ''   // drop legacy emoji values
  const ov = document.createElement('div')
  ov.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:700;display:flex;align-items:center;justify-content:center'
  const dlg = document.createElement('div')
  dlg.style.cssText = 'width:min(380px,92vw);background:var(--bg1);border:1px solid var(--border2);border-radius:12px;padding:16px;display:flex;flex-direction:column;gap:12px'
  dlg.innerHTML = `
    <div style="font-weight:600;color:var(--text)">Edit workspace</div>
    <label style="font-size:11px;color:var(--text2)">Name<input id="ws-name" type="text" value="${escAttr(sess.name)}" style="width:100%;margin-top:4px;background:var(--bg2);color:var(--text);border:1px solid var(--border2);border-radius:6px;padding:6px 8px"></label>
    <div style="font-size:11px;color:var(--text2)">Icon</div>
    <div id="ws-icons" style="display:grid;grid-template-columns:repeat(8,1fr);gap:6px;max-height:150px;overflow-y:auto"></div>
    <label style="font-size:11px;color:var(--text2)">Workspace adaptation
      <textarea id="ws-personality" rows="4" placeholder="Extra instructions for this workspace, layered on top of the default personality. Leave blank to use the default as-is." style="width:100%;margin-top:4px;background:var(--bg2);color:var(--text);border:1px solid var(--border2);border-radius:6px;padding:6px 8px;font:inherit;font-size:12px;resize:vertical;box-sizing:border-box"></textarea></label>
    <div style="font-size:10px;color:var(--text3);margin-top:-4px">Edit the default in Settings → Agent. Takes effect on this workspace's next message.</div>
    <div style="display:flex;gap:8px;justify-content:flex-end;margin-top:4px">
      <button id="ws-cancel" style="background:var(--bg2);border:1px solid var(--border2);color:var(--text2);border-radius:6px;padding:5px 12px;cursor:pointer">Cancel</button>
      <button id="ws-save" style="background:var(--accent);color:#fff;border:none;border-radius:6px;padding:5px 14px;cursor:pointer">Save</button>
    </div>`
  ov.appendChild(dlg); document.body.appendChild(ov)

  const grid = dlg.querySelector('#ws-icons')
  function paint() { grid.querySelectorAll('button').forEach(b => { b.style.borderColor = b.dataset.ic === selected ? 'var(--accent)' : 'var(--border2)'; b.style.color = b.dataset.ic === selected ? 'var(--accent)' : 'var(--text2)' }) }
  // "Default" (no icon) first, then all icons.
  for (const name of ['', ...Object.keys(WS_ICONS)]) {
    const b = document.createElement('button'); b.type = 'button'; b.dataset.ic = name
    b.style.cssText = 'aspect-ratio:1;background:var(--bg2);border:1px solid var(--border2);border-radius:7px;display:flex;align-items:center;justify-content:center;cursor:pointer;color:var(--text2)'
    b.innerHTML = name ? wsIconSvg(name).replace('viewBox', 'width="17" height="17" viewBox') : '<span style="font-size:10px">none</span>'
    b.onclick = () => { selected = name; paint() }
    grid.appendChild(b)
  }
  paint()

  // Load the workspace's stored personality (async — empty = default).
  const persoEl = dlg.querySelector('#ws-personality')
  let persoLoaded = ''
  fetch(`/api/personality?session=${encodeURIComponent(sess.id)}`)
    .then(r => r.json()).then(d => { persoLoaded = d.personality || ''; persoEl.value = persoLoaded })
    .catch(() => {})

  const close = () => ov.remove()
  ov.addEventListener('click', e => { if (e.target === ov) close() })
  dlg.querySelector('#ws-cancel').onclick = close
  dlg.querySelector('#ws-save').onclick = async () => {
    const name = dlg.querySelector('#ws-name').value.trim()
    setWorkspaceIcon(sess.id, selected)
    if (name && name !== sess.name) {
      await fetch(`/api/sessions/${sess.id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }) })
    }
    const perso = persoEl.value.trim()
    if (perso !== persoLoaded.trim()) {
      await fetch(`/api/personality?session=${encodeURIComponent(sess.id)}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ personality: perso }) })
    }
    close()
    loadSessions()
    if (currentView.type === 'board' && currentSessionID === sess.id) {
      const t = document.getElementById('view-title'); if (t && name) t.textContent = name
      setChatAgentLabel(name || sess.name)
    }
  }
  setTimeout(() => dlg.querySelector('#ws-name').focus(), 50)
}

// ─── Command palette (Ctrl/⌘-K) ─────────────────────────────────────────────────
function openCmdK() {
  if (document.getElementById('cmdk')) return
  const ov = document.createElement('div'); ov.id = 'cmdk'
  ov.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:800;display:flex;align-items:flex-start;justify-content:center;padding-top:12vh'
  const box = document.createElement('div')
  box.style.cssText = 'width:min(520px,92vw);background:var(--bg1);border:1px solid var(--border2);border-radius:12px;overflow:hidden;box-shadow:0 20px 60px rgba(0,0,0,.5)'
  box.innerHTML = `<input id="cmdk-input" placeholder="Jump to an app or workspace…" autocomplete="off" style="width:100%;box-sizing:border-box;background:var(--bg2);color:var(--text);border:none;border-bottom:1px solid var(--border);padding:12px 14px;font-size:14px;outline:none"><div id="cmdk-list" style="max-height:52vh;overflow-y:auto"></div>`
  ov.appendChild(box); document.body.appendChild(ov)
  const input = box.querySelector('#cmdk-input'), list = box.querySelector('#cmdk-list')
  const all = [
    ...Object.keys(APP_TITLES).map(n => ({ kind: 'app', name: n, label: APP_TITLES[n], icon: '✦' })),
    ...allSessions.filter(s => s.id !== ASSISTANT).map(s => ({ kind: 'board', id: s.id, label: s.name, icon: getWorkspaceIcon(s.id) || '▣' })),
  ]
  let filtered = all, sel = 0
  function render() {
    list.innerHTML = ''
    filtered.forEach((it, i) => {
      const row = document.createElement('div')
      row.style.cssText = `display:flex;align-items:center;gap:10px;padding:9px 14px;cursor:pointer;${i === sel ? 'background:var(--bg3)' : ''}`
      row.innerHTML = `<span style="width:20px;text-align:center">${it.icon}</span><span style="flex:1;color:var(--text);font-size:13px">${escAttr(it.label)}</span><span style="font-size:11px;color:var(--text3)">${it.kind === 'app' ? 'App' : 'Workspace'}</span>`
      row.onmouseenter = () => { sel = i; render() }
      row.onclick = () => choose(i)
      list.appendChild(row)
    })
  }
  function choose(i) { const it = filtered[i]; if (!it) return; close(); if (it.kind === 'app') setView({ type: 'app', name: it.name }); else selectBoard(it.id) }
  function close() { ov.remove(); document.removeEventListener('keydown', onKey, true) }
  function onKey(e) {
    if (e.key === 'Escape') { e.preventDefault(); close() }
    else if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(sel + 1, filtered.length - 1); render() }
    else if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(sel - 1, 0); render() }
    else if (e.key === 'Enter') { e.preventDefault(); choose(sel) }
  }
  input.addEventListener('input', () => { const q = input.value.toLowerCase(); filtered = all.filter(it => it.label.toLowerCase().includes(q)); sel = 0; render() })
  document.addEventListener('keydown', onKey, true)
  ov.addEventListener('click', e => { if (e.target === ov) close() })
  render(); input.focus()
}
document.addEventListener('keydown', e => {
  if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) { e.preventDefault(); openCmdK() }
})

// ─── Unread mail badge on the rail Email icon ────────────────────────────────────
async function refreshMailBadge() {
  try {
    const { count } = await (await fetch('/api/email/unread')).json()
    const iconEl = document.querySelector('.rail-item[data-app="email"] .rail-icon')
    if (!iconEl) return
    let b = iconEl.querySelector('.rail-badge')
    if (count > 0) {
      if (!b) { b = document.createElement('span'); b.className = 'rail-badge'; iconEl.appendChild(b) }
      b.textContent = count > 99 ? '99+' : String(count)
    } else if (b) { b.remove() }
  } catch {}
}

function renderBoardList(sessions) {
  const host = document.getElementById('rail-boards')
  if (!host) return
  host.innerHTML = ''
  for (const sess of sessions) {
    if (sess.id === ASSISTANT) continue   // shown as the dedicated top item
    const item = document.createElement('button')
    item.className = 'rail-item'
    item.dataset.boardId = sess.id
    if (currentView.type === 'board' && sess.id === currentSessionID) item.classList.add('active')
    item.title = sess.name
    item.onclick = () => selectBoard(sess.id)

    const icon = document.createElement('span')
    icon.className = 'rail-icon'
    const ic = getWorkspaceIcon(sess.id)
    if (ic && WS_ICONS[ic]) icon.innerHTML = wsIconSvg(ic)
    else if (ic) icon.textContent = ic                      // legacy emoji value
    else icon.innerHTML = wsIconSvg('layout')
    const label = document.createElement('span')
    label.className = 'rail-label'
    label.textContent = sess.name
    item.append(icon, label)

    const rename = document.createElement('span')
    rename.className = 'rail-board-act'
    rename.title = 'Edit'
    rename.textContent = '✎'
    rename.onclick = (e) => { e.stopPropagation(); editWorkspace(sess) }
    item.appendChild(rename)

    if (sess.id !== currentSessionID) {
      const del = document.createElement('span')
      del.className = 'rail-board-act'
      del.title = 'Delete'
      del.textContent = '×'
      del.onclick = async (e) => {
        e.stopPropagation()
        if (!confirm(`Delete workspace "${sess.name}" and all its history?`)) return
        await fetch(`/api/sessions/${sess.id}`, { method: 'DELETE' })
        loadSessions()
      }
      item.appendChild(del)
    }
    host.appendChild(item)
  }
}

function selectBoard(id) {
  setView({ type: 'board', workspace: id })
  loadSessions()
}

function promptNewSession() {
  let selected = ''
  const ov = document.createElement('div')
  ov.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:700;display:flex;align-items:center;justify-content:center'
  const dlg = document.createElement('div')
  dlg.style.cssText = 'width:min(380px,92vw);background:var(--bg1);border:1px solid var(--border2);border-radius:12px;padding:16px;display:flex;flex-direction:column;gap:12px'
  dlg.innerHTML = `
    <div style="font-weight:600;color:var(--text)">New workspace</div>
    <label style="font-size:11px;color:var(--text2)">Name<input id="nw-name" type="text" placeholder="e.g. Research" style="width:100%;margin-top:4px;background:var(--bg2);color:var(--text);border:1px solid var(--border2);border-radius:6px;padding:6px 8px;box-sizing:border-box"></label>
    <div style="font-size:11px;color:var(--text2)">Icon</div>
    <div id="nw-icons" style="display:grid;grid-template-columns:repeat(8,1fr);gap:6px;max-height:170px;overflow-y:auto"></div>
    <div class="nw-err" style="color:var(--red);font-size:12px;min-height:14px"></div>
    <div style="display:flex;gap:8px;justify-content:flex-end;margin-top:4px">
      <button id="nw-cancel" style="background:var(--bg2);border:1px solid var(--border2);color:var(--text2);border-radius:6px;padding:5px 12px;cursor:pointer">Cancel</button>
      <button id="nw-save" style="background:var(--accent);color:#fff;border:none;border-radius:6px;padding:5px 14px;cursor:pointer">Create</button>
    </div>`
  ov.appendChild(dlg); document.body.appendChild(ov)

  const grid = dlg.querySelector('#nw-icons')
  function paint() { grid.querySelectorAll('button').forEach(b => { b.style.borderColor = b.dataset.ic === selected ? 'var(--accent)' : 'var(--border2)'; b.style.color = b.dataset.ic === selected ? 'var(--accent)' : 'var(--text2)' }) }
  for (const name of ['', ...Object.keys(WS_ICONS)]) {
    const b = document.createElement('button'); b.type = 'button'; b.dataset.ic = name
    b.style.cssText = 'aspect-ratio:1;background:var(--bg2);border:1px solid var(--border2);border-radius:7px;display:flex;align-items:center;justify-content:center;cursor:pointer;color:var(--text2)'
    b.innerHTML = name ? wsIconSvg(name).replace('viewBox', 'width="17" height="17" viewBox') : '<span style="font-size:10px">none</span>'
    b.onclick = () => { selected = name; paint() }
    grid.appendChild(b)
  }
  paint()

  const close = () => ov.remove()
  ov.addEventListener('click', e => { if (e.target === ov) close() })
  dlg.querySelector('#nw-cancel').onclick = close
  dlg.querySelector('#nw-save').onclick = async () => {
    const name = dlg.querySelector('#nw-name').value.trim()
    if (!name) { dlg.querySelector('.nw-err').textContent = 'Name is required.'; return }
    try {
      const res = await fetch('/api/sessions', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name })
      })
      const data = await res.json()
      if (selected) setWorkspaceIcon(data.id, selected)
      close()
      setView({ type: 'board', workspace: data.id })
      loadSessions()
    } catch (e) {
      dlg.querySelector('.nw-err').textContent = 'Failed: ' + e.message
    }
  }
  setTimeout(() => dlg.querySelector('#nw-name').focus(), 50)
}

// Wire the rail app buttons.
document.querySelectorAll('.rail-item[data-app]').forEach(btn => {
  btn.addEventListener('click', () => setView({ type: 'app', name: btn.dataset.app }))
})

window.promptNewSession = promptNewSession

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
  } else if (d.type === 'context' && typeof d.text === 'string') {
    setContext(d.text)
  } else if (d.type === 'mail-unread-changed') {
    refreshMailBadge()
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

  // While dragging, the cursor moves over the app/widget iframes which would
  // otherwise swallow mousemove events and make the drag stutter.
  const setIframePE = on => document.querySelectorAll('iframe').forEach(f => { f.style.pointerEvents = on ? '' : 'none' })

  handle.addEventListener('mousedown', e => {
    resizing = true
    startX = e.clientX
    startWidth = chatDrawer.offsetWidth
    handle.classList.add('dragging')
    document.body.style.cursor = 'ew-resize'
    document.body.style.userSelect = 'none'
    setIframePE(false)
    e.preventDefault()
  })

  document.addEventListener('mousemove', e => {
    if (!resizing) return
    const newWidth = Math.min(Math.max(startWidth + (startX - e.clientX), 300), 720)
    chatDrawer.style.width = newWidth + 'px'
  })

  document.addEventListener('mouseup', () => {
    if (!resizing) return
    resizing = false
    handle.classList.remove('dragging')
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    setIframePE(true)
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

  window.PrismTheme.populateSelect(document.getElementById('theme-select'))
  renderDock()
  updateSettingsLink()
  loadSessions()
  setView({ type: 'board', workspace: lastWorkspace })
  connect()
  loadModels()
  setInterval(loadModels, 30000)
  refreshMailBadge()
  setInterval(refreshMailBadge, 60000)

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      if (editorPath) closeEditor()
      else if (notifPanelOpen) toggleNotifPanel()
      else if (chatOpen) toggleChat()
    }
  })
}

initApp()
