// Shared by personal Settings and deployment Admin. Credentials stay in memory until save.
async function renderAITab(container, options = {}) {
  container.replaceChildren()
  const page = document.createElement('div')
  page.className = options.admin ? 'ai-settings-page' : 'settings-page ai-settings-page'
  const pane = document.createElement('div')
  pane.className = 'ai-settings'
  page.appendChild(pane); container.appendChild(page)
  const providers = '<option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="ollama">Ollama</option><option value="other">Other compatible</option>'
  const defaults = {openai:'https://api.openai.com/v1',anthropic:'https://api.anthropic.com',ollama:'',other:''}
  const listID = prefix => `${options.admin ? 'admin-' : ''}ai-${prefix}models`
  const connection = prefix => `
    <label class="ai-field">Provider<select name="${prefix}provider">${providers}</select></label>
    <p class="ai-hint" data-hint="${prefix}"></p>
    <div class="ai-key" data-key="${prefix}">
      <div class="ai-key-heading"><span>API key</span><span class="ai-key-state" data-key-status="${prefix}">Not set</span><div class="ai-key-actions">
        <button type="button" class="ai-link" data-replace-key="${prefix}">Replace</button><button type="button" class="ai-link danger" data-remove-key="${prefix}">Remove</button><button type="button" class="ai-link" data-cancel-key="${prefix}">Cancel</button>
      </div></div>
      <label class="ai-field ai-key-input" data-key-input="${prefix}"><span class="ai-sr-only">${prefix ? 'Embedding API key' : 'API key'}</span><input name="${prefix}apiKey" type="password" autocomplete="new-password" placeholder="Paste your API key" spellcheck="false"></label>
      <input name="${prefix}clearKey" type="checkbox" hidden>
    </div>
    <details class="ai-endpoint" data-endpoint="${prefix}" data-connection-advanced="${prefix}"><summary><span data-endpoint-title="${prefix}">Advanced connection</span><span class="ai-endpoint-label" data-endpoint-label="${prefix}"></span></summary>
      <label class="ai-field">Server URL<input name="${prefix}baseURL" type="url" spellcheck="false" placeholder="http://your-server:11434"></label>
    </details>`
  const model = prefix => `
    <label class="ai-field">${prefix ? 'Embedding model' : 'Model'}<input name="${prefix}model" list="${listID(prefix)}" placeholder="Enter or choose a model ID" spellcheck="false"><datalist id="${listID(prefix)}"></datalist></label>`
  pane.innerHTML = `
    <div class="ai-header">
      <div class="ai-title-line"><h2>AI provider</h2><span class="ai-scope">${options.admin ? 'Deployment' : 'Personal'}</span></div>
      <p class="ai-description" data-source>Loading configuration…</p>
      <div class="ai-current"><span>Active default</span><strong data-current-model>Not configured</strong><small data-current-source></small></div>
    </div>
    <form autocomplete="off" hidden>
      <section class="ai-section">
        <div class="ai-section-heading"><h3>Connections</h3><button class="ai-button" type="button" data-add-source>Add connection</button></div>
        <div class="ai-layout">
          <div class="ai-sources" data-sources aria-label="AI connections"></div>
          <div class="ai-source-editor">
            <div class="ai-editor-heading"><strong data-editor-title>Connection</strong><button class="ai-link danger" type="button" data-remove-source>Remove connection</button></div>
            <label class="ai-field">Connection name<input name="sourceName" maxlength="100" placeholder="e.g. Local Ollama"></label>
            ${connection('')}
            <div class="ai-verify"><button class="ai-button" type="button" data-action="models">Verify connection</button><span class="ai-connect-status" data-connected="" role="status"></span></div>
            <div class="ai-model-section">${model('')}
              <div class="ai-model-settings-row"><span class="ai-hint" data-model-capabilities></span><button type="button" class="ai-link" data-model-settings>Model settings</button></div>
            </div>
            <div class="ai-default-action"><span data-default-action-help></span><button class="ai-button" type="button" data-default-source>Use as default</button></div>
            <details class="ai-advanced"><summary>Test a response</summary><p class="ai-hint">Sends a short model request. API charges may apply.</p><button class="ai-button" type="button" data-action="test">Test model</button></details>
          </div>
        </div>
        <input name="chatVision" type="checkbox" hidden aria-label="Legacy vision fallback">
      </section>
      <details class="ai-section ai-embedding-section" data-embedding-section>
        <summary><span>Document search</span><span class="ai-section-summary" data-embedding-summary>Optional</span></summary>
        <div class="ai-details-body">
          <p class="ai-hint">Choose an embedding model to search indexed documents.</p>
          <label class="ai-field">Connection<select name="embeddingMode"><option value="disabled">Disabled</option><option value="default">Use default conversation connection</option><option value="source">Use another connection</option><option value="dedicated">Dedicated embedding connection</option></select></label>
          <input name="useSameProvider" type="checkbox" hidden>
          <div class="ai-embedding-config" data-embedding-config>
            <label class="ai-field" data-embedding-source>Connection<select name="embeddingSource"></select></label>
            <div data-embedding-connection>${connection('embed-')}</div>
            <p class="ai-hint error" data-unsupported hidden>Anthropic does not provide embeddings. Choose another connection.</p>
            ${model('embed-')}
            <div class="ai-verify"><button class="ai-button" type="button" data-action="embedding_models">Load models</button><span class="ai-connect-status" data-connected="embed-" role="status"></span></div>
            <details class="ai-advanced"><summary>Test embeddings</summary><p class="ai-hint">Sends a test embedding request. API charges may apply.</p><button class="ai-button" type="button" data-action="embedding_test">Test embedding model</button></details>
          </div>
          <label class="ai-reindex" data-reindex hidden><input name="reindex" type="checkbox"><span>Rebuild the document index when saving<small>Indexed text will be sent to this provider. API charges may apply.</small></span></label>
          <p class="ai-hint" data-embedding-status role="status"></p>
        </div>
      </details>
      <details class="ai-server-defaults" data-server-defaults hidden><summary>Server defaults</summary><p class="ai-hint">Restore the deployment’s environment configuration. An embedding change may rebuild the document index.</p><button class="ai-button" type="button" data-action="reset">Restore server defaults</button></details>
      <div class="ai-footer">
        <div class="ai-save-copy"><span class="ai-save-state" data-save-state role="status">Loading…</span><p class="ai-status" data-status role="status"></p></div>
        <div class="ai-actions"><button class="ai-button" type="button" data-discard>Discard changes</button><button class="ai-button primary" type="submit" data-save>Save changes</button></div>
      </div>
    </form>
    <p class="ai-status error" data-load-error hidden></p>
    <dialog class="ai-model-dialog" aria-labelledby="ai-model-title"><form data-model-form>
      <h3 id="ai-model-title">Model settings</h3><p class="ai-hint" data-model-source></p>
      <label class="ai-field">Model<input name="settingsModel" list="${listID('settings-')}" required maxlength="200" spellcheck="false"><datalist id="${listID('settings-')}"></datalist></label>
      <label class="ai-field">Context window<select name="contextMode"><option value="auto">Automatic</option><option value="manual">Set token limit</option></select></label>
      <label class="ai-field" data-context-limit hidden>Context window (tokens)<input name="contextWindow" type="number" min="4096" max="2000000" step="1"></label><p class="ai-hint" data-context-detected></p>
      <label class="ai-field">Generation limit (tokens)<input name="maxOutputTokens" type="number" min="1" max="2000000" step="1" placeholder="16384 (default)"></label><p class="ai-hint">Includes reasoning, replies and tool arguments. Leave blank for the default.</p>
      <label class="ai-field">Vision support<select name="modelVision"><option value="auto">Automatic</option><option value="yes">Supports images</option><option value="no">Text only</option></select></label><p class="ai-hint" data-vision-detected></p>
      <p class="ai-status" data-model-status role="status"></p>
      <div class="ai-model-footer"><button class="ai-link" type="button" data-model-reset>Reset to automatic</button><div><button class="ai-button" type="button" data-model-close>Cancel</button><button class="ai-button primary" type="submit">Apply to changes</button></div></div>
    </form></dialog>`
  const form = pane.querySelector('form'), status = pane.querySelector('[data-status]')
  const f = name => form.elements.namedItem(name)
  const clone = value => JSON.parse(JSON.stringify(value))
  let saved = null, busy = false, sources = [], selectedID = null, defaultID = null, dirty = false, resetPending = false, reloadRequired = false
  const embeddingKey = Symbol('embedding')
  const catalogs = new Map(), checks = new Map(), modelInfo = new Map(), replacing = new Set()
  let capabilityTimer = 0
  const confirm = message => typeof PrismModal !== 'undefined' ? PrismModal.confirm(message) : Promise.resolve(window.confirm(message))
  const warnOnLeave = e => { if (pane.isConnected && (dirty || busy)) { e.preventDefault(); e.returnValue = '' } }
  const warnOnNavigate = e => { if (pane.isConnected && (dirty || busy) && e.target.closest('a[href],.nav-item') && !pane.contains(e.target) && !window.confirm('You have unsaved AI settings. Leave this page?')) { e.preventDefault(); e.stopImmediatePropagation() } }
  window.addEventListener('beforeunload', warnOnLeave); document.addEventListener('click', warnOnNavigate, true)
  const cleanup = new MutationObserver(() => { if (!pane.isConnected) { monitorGeneration++; clearTimeout(capabilityTimer); window.removeEventListener('beforeunload', warnOnLeave); document.removeEventListener('click', warnOnNavigate, true); cleanup.disconnect() } })
  cleanup.observe(container, {childList:true})
  const providerName = id => ({openai:'OpenAI',anthropic:'Anthropic',ollama:'Ollama',other:'Compatible API'})[id] || id
  const catalogKey = prefix => prefix ? embeddingKey : selectedID
  const keyID = prefix => prefix ? embeddingKey : selectedID
  const source = () => sources.find(s => s.id === selectedID)
  const prior = prefix => prefix ? saved?.embedding : saved?.sources?.find(s => s.id === selectedID)
  const sameConnection = (a, b) => !!a && !!b && a.provider === b.provider && a.baseURL?.replace(/\/+$/, '') === b.baseURL?.replace(/\/+$/, '')
  const identity = e => JSON.stringify([e.provider === 'other' ? 'openai' : e.provider, (e.baseURL || '').replace(/\/+$/, ''), (e.model || '').trim()])
  function markDirty() { dirty = true; showSaveState() }
  function showSaveState() {
    const state = pane.querySelector('[data-save-state]')
    state.textContent = busy ? 'Working…' : reloadRequired ? 'Changes saved · reload settings to continue' : resetPending ? 'Server defaults will be restored when you save.' : dirty ? 'Unsaved changes' : 'All changes saved'
    state.classList.toggle('pending', dirty)
    pane.querySelector('[data-save]').disabled = busy || reloadRequired || !dirty
    pane.querySelector('[data-discard]').disabled = busy || (!dirty && !reloadRequired)
    pane.querySelector('[data-discard]').textContent = reloadRequired ? 'Reload settings' : 'Discard changes'
    pane.querySelector('[data-current-model]').textContent = saved?.model || 'Not configured'
    pane.querySelector('[data-current-source]').textContent = saved?.sources?.find(s => s.id === saved.defaultSource)?.name || ''
  }
  function syncSource() {
    const src = source()
    if (src) Object.assign(src, {name:f('sourceName').value,provider:f('provider').value,baseURL:f('baseURL').value,apiKey:f('apiKey').value,clearKey:f('clearKey').checked,model:f('model').value})
  }
  function draftEmbedding() {
    const mode = f('embeddingMode').value
    const dedicatedKey = mode === 'dedicated' || mode === 'disabled'
    return {useSameProvider:mode === 'default',sourceID:mode === 'source' ? f('embeddingSource').value : '',provider:f('embed-provider').value,baseURL:f('embed-baseURL').value,apiKey:dedicatedKey ? f('embed-apiKey').value : '',clearKey:dedicatedKey && f('embed-clearKey').checked,model:mode === 'disabled' ? '' : f('embed-model').value,reindex:f('reindex').checked}
  }
  function effective() {
    const e = draftEmbedding(), mode = f('embeddingMode').value
    const src = mode === 'default' ? sources.find(s => s.id === defaultID) : mode === 'source' ? sources.find(s => s.id === e.sourceID) : null
    return src ? {...e,provider:src.provider,baseURL:src.baseURL} : e
  }
  function payload(action) {
    syncSource()
    const probing = action === 'models' || action === 'test'
    const primary = sources.find(s => s.id === (probing ? selectedID : defaultID)) || sources[0]
    return {action,sources:(probing ? [primary] : sources).map(s => ({...s})),defaultSource:primary.id,sourceID:probing ? selectedID : undefined,provider:primary.provider,baseURL:primary.baseURL,apiKey:primary.apiKey,model:primary.model,chatVision:f('chatVision').checked,embedding:draftEmbedding()}
  }
  function showModels(prefix) {
    const list = pane.querySelector('#' + listID(prefix)); list.replaceChildren()
    for (const name of new Set([...(catalogs.get(catalogKey(prefix)) || []),f(prefix+'model').value].filter(Boolean))) { const option = document.createElement('option'); option.value = name; list.appendChild(option) }
  }
  function drawSources() {
    const list = pane.querySelector('[data-sources]'); list.replaceChildren()
    for (const src of sources) {
      const row = document.createElement('button'); row.type = 'button'; row.className = 'ai-source' + (src.id === selectedID ? ' selected' : ''); row.setAttribute('aria-pressed', String(src.id === selectedID))
      const title = document.createElement('span'); title.className = 'ai-source-title'
      const name = document.createElement('strong'); name.textContent = src.name || providerName(src.provider); title.appendChild(name)
      if (src.id === defaultID) { const badge = document.createElement('em'); badge.className = 'ai-default-badge'; badge.textContent = 'Default'; title.appendChild(badge) }
      const detail = document.createElement('small'); detail.textContent = providerName(src.provider) + ' · ' + (src.model || 'No model')
      const stored = saved?.sources?.find(s => s.id === src.id)
      const pending = !sameConnection(src,stored) || src.name !== stored?.name || src.model !== stored?.model || !!src.apiKey || src.clearKey || JSON.stringify(src.modelSettings || {}) !== JSON.stringify(stored?.modelSettings || {})
      const needsKey = (src.provider === 'anthropic' || src.provider === 'openai') && !stored?.keyConfigured && !src.apiKey
      const connection = document.createElement('small'); connection.className = 'ai-source-state'; connection.textContent = pending ? checks.get(src.id)?.ok ? 'Verified · not saved' : 'Not saved' : checks.get(src.id)?.ok ? 'Verified' : needsKey ? 'API key needed' : 'Saved connection'
      row.append(title, detail, connection); row.disabled = busy || resetPending || reloadRequired; row.addEventListener('click', () => { syncSource(); editSource(src.id) }); list.appendChild(row)
    }
    const current = f('embeddingSource').value; f('embeddingSource').replaceChildren()
    for (const src of sources) { const option = document.createElement('option'); option.value = src.id; option.textContent = src.name || providerName(src.provider); f('embeddingSource').appendChild(option) }
    f('embeddingSource').value = sources.some(s => s.id === current) ? current : defaultID
  }
  function editSource(id) {
    selectedID = id
    const src = source()
    for (const field of ['provider','baseURL','apiKey','model']) f(field).value = src[field] || ''
    f('sourceName').value = src.name || ''; f('clearKey').checked = !!src.clearKey
    showModels(''); drawSources(); refresh()
  }
  function invalidate(prefix) {
    catalogs.delete(catalogKey(prefix)); checks.delete(catalogKey(prefix)); showModels(prefix)
    pane.querySelector(`[data-connected="${prefix}"]`).textContent = ''
  }
  function refreshKey(prefix) {
    const provider = f(prefix+'provider').value
    const stored = prior(prefix), same = sameConnection(stored, {provider,baseURL:f(prefix+'baseURL').value})
    const hasSaved = same && stored?.keyConfigured, removed = f(prefix+'clearKey').checked
    const editing = replacing.has(keyID(prefix)) || !!f(prefix+'apiKey').value
    pane.querySelector(`[data-key="${prefix}"]`).hidden = provider === 'ollama'
    pane.querySelector(`[data-key-status="${prefix}"]`).textContent = removed ? 'Removal pending' : editing ? hasSaved ? 'Replacement pending' : 'Not saved' : hasSaved ? 'Saved securely' : 'Not set'
    pane.querySelector(`[data-key-input="${prefix}"]`).hidden = removed || (hasSaved && !editing)
    pane.querySelector(`[data-replace-key="${prefix}"]`).hidden = !hasSaved || editing || removed
    pane.querySelector(`[data-remove-key="${prefix}"]`).hidden = !hasSaved || editing || removed
    pane.querySelector(`[data-cancel-key="${prefix}"]`).hidden = !hasSaved || (!editing && !removed)
  }
  function modelTokenLabel(n) { n = Number(n); return n >= 1000000 && n % 1000000 === 0 ? `${n/1000000}M` : n >= 1000 && n % 1000 === 0 ? `${n/1000}k` : n.toLocaleString('en-US') }
  const modelInfoKey = (src, model) => JSON.stringify([src.id,src.provider,src.baseURL,model])
  async function fetchModelInfo(sourceID, model) {
    return requestData({action:'model_info',sourceID,model})
  }
  function showModelCapabilities() {
    const src = source(), stored = saved?.sources?.find(s => s.id === selectedID), model = f('model').value.trim(), el = pane.querySelector('[data-model-capabilities]')
    if (!model) { el.textContent = ''; return }
    const cfg = src?.modelSettings?.[model] || {}, key = modelInfoKey(src,model)
    let entry = modelInfo.get(key)
    if (!entry && sameConnection(src,stored)) {
      entry = {loading:true}; modelInfo.set(key,entry)
      fetchModelInfo(src.id,model).then(data => { entry.data = data }).catch(() => { entry.failed = true }).finally(() => { entry.loading = false; if (pane.isConnected && modelInfo.get(key) === entry) refresh() })
    }
    const detected = entry?.data?.detected || {}
    const context = cfg.contextWindow ? `${modelTokenLabel(cfg.contextWindow)} tokens (manual)` : detected.contextWindow ? `${modelTokenLabel(detected.contextWindow)} tokens (auto-detected)` : entry?.loading ? 'checking…' : 'not detected (conservative limit)'
    const vision = typeof cfg.vision === 'boolean' ? `${cfg.vision ? 'supported' : 'text only'} (manual)` : typeof detected.vision === 'boolean' ? `${detected.vision ? 'supported' : 'text only'} (auto-detected)` : entry?.loading ? 'checking…' : 'not detected (existing setting)'
    el.textContent = `Context: ${context} · Vision: ${vision}`
  }
  function refresh() {
    for (const prefix of ['', 'embed-']) {
      const provider = f(prefix+'provider').value, url = f(prefix+'baseURL').value.replace(/\/+$/, ''), local = provider === 'ollama' || provider === 'other', custom = !!url && url !== defaults[provider]
      const endpoint = pane.querySelector(`[data-endpoint="${prefix}"]`)
      endpoint.classList.toggle('required', local)
      endpoint.querySelector('summary').tabIndex = local ? -1 : 0
      if (local) endpoint.open = true
      pane.querySelector(`[data-endpoint-title="${prefix}"]`).textContent = local ? 'Server address' : 'Advanced connection'
      pane.querySelector(`[data-endpoint-label="${prefix}"]`).textContent = !local && custom ? 'Custom endpoint · ' + url : !local ? defaults[provider] : ''
      pane.querySelector(`[data-hint="${prefix}"]`).textContent = provider === 'other' ? 'OpenAI-compatible API, such as vLLM, SGLang or llama.cpp.' : provider === 'ollama' ? 'Use the address reachable from the Prism server.' : ''
      f(prefix+'baseURL').placeholder = provider === 'other' ? 'http://your-server:8000/v1' : provider === 'ollama' ? 'http://your-server:11434' : defaults[provider]
      refreshKey(prefix)
    }
    const src = source(), mode = f('embeddingMode').value
    pane.querySelector('[data-editor-title]').textContent = src?.name || 'New connection'
    pane.querySelector('[data-remove-source]').hidden = sources.length === 1
    pane.querySelector('[data-default-source]').hidden = selectedID === defaultID
    pane.querySelector('[data-default-source]').disabled = busy || resetPending || reloadRequired || !src?.model?.trim()
    pane.querySelector('[data-default-action-help]').textContent = selectedID === defaultID ? 'Default conversation connection' : 'Use this connection for new conversations.'
    pane.querySelector('[data-model-settings]').disabled = busy || resetPending || reloadRequired || !f('model').value.trim()
    pane.querySelector('[data-embedding-config]').hidden = mode === 'disabled'
    pane.querySelector('[data-embedding-source]').hidden = mode !== 'source'
    pane.querySelector('[data-embedding-connection]').hidden = mode !== 'dedicated'
    f('useSameProvider').checked = mode === 'default'
    const unsupported = mode !== 'disabled' && effective().provider === 'anthropic'
    pane.querySelector('[data-unsupported]').hidden = !unsupported
    pane.querySelectorAll('[data-action^="embedding_"]').forEach(button => { button.disabled = busy || resetPending || reloadRequired || mode === 'disabled' || unsupported })
    pane.querySelector('[data-reindex]').hidden = mode === 'disabled' || !saved || (identity(effective()) === identity(saved.embedding) && !saved.embeddingStatus?.includes('failed'))
    pane.querySelector('[data-embedding-summary]').textContent = mode === 'disabled' ? 'Disabled' : f('embed-model').value || 'Choose a model'
    for (const prefix of ['', 'embed-']) { const check = checks.get(catalogKey(prefix)); pane.querySelector(`[data-connected="${prefix}"]`).textContent = check?.text || ''; pane.querySelector(`[data-connected="${prefix}"]`).classList.toggle('error', check?.ok === false) }
    clearTimeout(capabilityTimer); capabilityTimer = setTimeout(() => { if (pane.isConnected) showModelCapabilities() },250)
    showSaveState()
  }
  async function fetchConfig(url = '/api/ai/config', options = {}, controls = null) {
    const controller = new AbortController(), timer = setTimeout(() => controller.abort(),40000)
    try {
      const response = await (globalThis.PrismSettingsState?.request || fetch)(url,{...options,signal:controller.signal},controls)
      const text = await response.text()
      return {ok:response.ok,status:response.status,text:async()=>text,json:async()=>JSON.parse(text)}
    } catch (error) {
      if (controller.signal.aborted) { const timeout = new Error('The server did not respond in time. Reopen settings to check whether your changes were saved before trying again.'); timeout.name = 'TimeoutError'; throw timeout }
      throw error
    } finally { clearTimeout(timer) }
  }
  async function requestData(data, controls = null) {
    const response = await fetchConfig('/api/ai/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)},controls)
    const text = await response.text(); let result
    try { result = JSON.parse(text) } catch { result = {} }
    if (!response.ok) throw new Error(result.error || text || 'Request failed')
    return result
  }
  function setBusy(value) {
    busy = value; pane.classList.toggle('is-busy',value)
    form.querySelectorAll('input,select,button').forEach(el => { el.disabled = value || resetPending || reloadRequired })
    if (resetPending && !value) { pane.querySelector('[data-save]').disabled = false; pane.querySelector('[data-discard]').disabled = false }
    if (!value) refresh(); else showSaveState()
  }
  let monitorGeneration = 0
  function showEmbeddingStatus(data) {
    pane.querySelector('[data-embedding-status]').textContent = data.embeddingApplying ? 'Applying embeddings — ' + data.embeddingStatus + '. Document search is temporarily unavailable.' : data.embeddingStatus || 'Not configured'
    saved.embeddingStatus = data.embeddingStatus; saved.embeddingPending = data.embeddingPending
  }
  async function monitorEmbedding(generation) {
    while (pane.isConnected && generation === monitorGeneration) {
      await new Promise(resolve => setTimeout(resolve,1000))
      if (!pane.isConnected || generation !== monitorGeneration) return
      try { const response = await fetchConfig('/api/ai/config',{cache:'no-store'}); if (!response.ok) throw new Error(); const data = await response.json(); showEmbeddingStatus(data); if (!data.embeddingApplying) return }
      catch { if (pane.isConnected) pane.querySelector('[data-embedding-status]').textContent = 'Cannot refresh document search status. Reopen settings to check.'; return }
    }
  }
  async function load() {
    const response = await fetchConfig('/api/ai/config',{cache:'no-store'})
    if (!response.ok) throw new Error('AI configuration is unavailable. In multi-user mode, use Admin → AI provider.')
    saved = await response.json(); modelInfo.clear(); replacing.clear(); checks.clear(); catalogs.clear()
    sources = saved.sources.map(src => ({...clone(src),apiKey:'',clearKey:false})); defaultID = saved.defaultSource
    selectedID = sources.some(src => src.id === selectedID) ? selectedID : defaultID
    for (const key of ['provider','baseURL','model']) f('embed-'+key).value = saved.embedding[key] || (key === 'provider' ? 'openai' : '')
    f('embed-apiKey').value = ''; f('embed-clearKey').checked = false
    f('embeddingMode').value = !saved.embedding.model ? 'disabled' : saved.embedding.useSameProvider !== false ? 'default' : saved.embedding.sourceID ? 'source' : 'dedicated'
    f('chatVision').checked = !!saved.chatVision; f('reindex').checked = false
    resetPending = false; dirty = false; reloadRequired = false
    editSource(selectedID); f('embeddingSource').value = saved.embedding.sourceID || defaultID
    pane.querySelector('[data-server-defaults]').hidden = saved.serverDefaultsAvailable === false
    pane.querySelector('[data-source]').textContent = options.admin ? 'Applies to all users and shared agents.' : 'Configure the models used by your agent and apps.'
    showEmbeddingStatus(saved); const generation = ++monitorGeneration; if (saved.embeddingApplying) void monitorEmbedding(generation)
    showModels('embed-'); refresh()
    globalThis.PrismSettingsState?.reset?.(pane.querySelectorAll('input,select,textarea'))
  }
  async function perform(action) {
    if (busy) return
    if (action === 'reset') {
      if (!await confirm('Restore server AI defaults when you save? An embedding change may rebuild the document index and send indexed text to the configured provider.')) return
      resetPending = true; dirty = true; status.textContent = ''; setBusy(false); return
    }
    // Capture values before disabling controls or starting request feedback.
    const data = action === 'save' && resetPending ? null : payload(action)
    const prefix = action.startsWith('embedding_') ? 'embed-' : '', checkKey = catalogKey(prefix)
    if (action === 'save' && !resetPending && !form.reportValidity()) return
    if (action === 'save' && !resetPending && f('embeddingMode').value !== 'disabled' && effective().provider === 'anthropic') {
      status.textContent = 'Choose a provider that supports embeddings, or disable document search.'; status.classList.add('error'); pane.querySelector('[data-embedding-section]').open = true; return
    }
    const controls = action === 'save' ? [...form.querySelectorAll('input,select,textarea')] : null
    setBusy(true); status.classList.remove('error'); status.textContent = action === 'save' ? 'Saving…' : action.endsWith('models') ? 'Verifying connection…' : 'Testing…'
    let persisted = false
    try {
      let result
      if (action === 'save' && resetPending) { const response = await fetchConfig('/api/ai/config?reindex=true',{method:'DELETE'},controls); result = await response.json(); if (!response.ok) throw new Error(result.error || 'Cannot restore server defaults') }
      else result = await requestData(data,controls)
      if (action.endsWith('models')) {
        catalogs.set(checkKey,result.models || []); showModels(prefix)
        checks.set(checkKey,{ok:true,text:result.models?.length ? `Connection verified · ${result.models.length} models` : 'Connection verified · enter a model ID'})
        status.textContent = ''; drawSources()
      } else if (action === 'test') status.textContent = 'The model responded. Changes have not been saved.'
      else if (action === 'embedding_test') status.textContent = `Embedding model verified · ${result.dimension} dimensions. Changes have not been saved.`
      else {
        persisted = true; dirty = false; resetPending = false
        for (const src of sources) src.apiKey = ''
        f('apiKey').value = ''; f('embed-apiKey').value = ''
        globalThis.PrismSettingsState?.reset?.(pane.querySelectorAll('input,select,textarea'))
        await load(); status.textContent = 'Changes saved.'
      }
    } catch (error) {
      if (persisted) reloadRequired = true
      status.textContent = (persisted ? 'Saved, but unable to refresh. ' : action === 'save' && error.name !== 'TimeoutError' ? 'Not saved: ' : '') + error.message; status.classList.add('error')
      if (/embedding|document[ -]search|document index|indexed text/i.test(error.message)) pane.querySelector('[data-embedding-section]').open = true
      if (action.endsWith('models')) checks.set(checkKey,{ok:false,text:error.message})
    } finally { setBusy(false) }
  }
  const dialog = pane.querySelector('dialog'), modelForm = dialog.querySelector('form'), mf = name => modelForm.elements.namedItem(name), modelStatus = dialog.querySelector('[data-model-status]')
  let modelSourceID = null, modelGeneration = 0
  function contextFields() { const manual = mf('contextMode').value === 'manual'; dialog.querySelector('[data-context-limit]').hidden = !manual; mf('contextWindow').required = manual }
  async function readModelInfo() {
    const generation = ++modelGeneration, src = sources.find(s => s.id === modelSourceID), model = mf('settingsModel').value.trim()
    const cfg = src?.modelSettings?.[model] || {}
    mf('contextMode').value = cfg.contextWindow ? 'manual' : 'auto'; mf('contextWindow').value = cfg.contextWindow || ''; mf('maxOutputTokens').value = cfg.maxOutputTokens || ''; mf('modelVision').value = cfg.vision === true ? 'yes' : cfg.vision === false ? 'no' : 'auto'; contextFields()
    globalThis.PrismSettingsState?.reset?.(modelForm.querySelectorAll('input,select,textarea'))
    modelStatus.textContent = ''; const stored = saved.sources.find(s => s.id === src?.id)
    let data = modelInfo.get(modelInfoKey(src,model))?.data
    if (!data && sameConnection(src,stored)) { try { data = await fetchModelInfo(src.id,model); modelInfo.set(modelInfoKey(src,model),{data,loading:false}) } catch { /* Manual settings remain usable without metadata. */ } }
    if (generation !== modelGeneration || !dialog.open) return
    const detected = data?.detected || {}
    mf('contextMode').options[0].textContent = detected.contextWindow ? `Automatic — ${modelTokenLabel(detected.contextWindow)} tokens` : 'Automatic — not detected'
    mf('modelVision').options[0].textContent = typeof detected.vision === 'boolean' ? `Automatic — ${detected.vision ? 'supports images' : 'text only'}` : 'Automatic — existing setting'
    dialog.querySelector('[data-context-detected]').textContent = detected.contextWindow ? `Provider context: ${Number(detected.contextWindow).toLocaleString('en-US')} tokens.` : 'Context was not reported. Enter the server’s effective limit if known.'
    dialog.querySelector('[data-vision-detected]').textContent = detected.vision === true ? 'The provider reports image support.' : detected.vision === false ? 'The provider reports a text-only model.' : 'Image support was not reported.'
  }
  pane.querySelector('[data-model-settings]').addEventListener('click', () => {
    syncSource(); modelSourceID = selectedID; const src = source()
    dialog.querySelector('[data-model-source]').textContent = (src.name || providerName(src.provider)) + ' · ' + providerName(src.provider)
    const list = dialog.querySelector('datalist'); list.replaceChildren()
    for (const id of new Set([src.model,...(catalogs.get(src.id) || []),...Object.keys(src.modelSettings || {})].filter(Boolean))) { const option = document.createElement('option'); option.value = id; list.appendChild(option) }
    mf('settingsModel').value = src.model; dialog.showModal(); void readModelInfo()
  })
  mf('settingsModel').addEventListener('change', () => { if (mf('settingsModel').value.trim()) void readModelInfo() })
  mf('contextMode').addEventListener('change',contextFields)
  dialog.querySelector('[data-model-reset]').addEventListener('click', () => { mf('contextMode').value = 'auto'; mf('contextWindow').value = ''; mf('maxOutputTokens').value = ''; mf('modelVision').value = 'auto'; contextFields() })
  dialog.querySelector('[data-model-close]').addEventListener('click', () => dialog.close())
  dialog.addEventListener('close', () => { modelGeneration++; globalThis.PrismSettingsState?.reset?.(modelForm.querySelectorAll('input,select,textarea')) })
  modelForm.addEventListener('submit', e => {
    e.preventDefault(); if (!modelForm.reportValidity()) return
    const settings = {contextWindow:mf('contextMode').value === 'manual' ? Number(mf('contextWindow').value) : 0,maxOutputTokens:mf('maxOutputTokens').value === '' ? 0 : Number(mf('maxOutputTokens').value)}
    if (mf('modelVision').value !== 'auto') settings.vision = mf('modelVision').value === 'yes'
    const src = sources.find(s => s.id === modelSourceID), model = mf('settingsModel').value.trim(); src.modelSettings = {...(src.modelSettings || {})}
    if (!settings.contextWindow && !settings.maxOutputTokens && settings.vision === undefined) delete src.modelSettings[model]; else src.modelSettings[model] = settings
    markDirty(); refresh(); dialog.close()
  })
  for (const prefix of ['', 'embed-']) {
    f(prefix+'provider').addEventListener('change', () => { f(prefix+'baseURL').value = defaults[f(prefix+'provider').value]; f(prefix+'apiKey').value = ''; f(prefix+'model').value = ''; f(prefix+'clearKey').checked = false; replacing.delete(keyID(prefix)); f('reindex').checked = false; invalidate(prefix); syncSource(); refresh() })
    for (const key of ['baseURL','apiKey']) f(prefix+key).addEventListener('input', () => invalidate(prefix))
    f(prefix+'model').addEventListener('input', () => { if (!prefix) syncSource(); refresh() })
    pane.querySelector(`[data-replace-key="${prefix}"]`).addEventListener('click', () => { replacing.add(keyID(prefix)); f(prefix+'clearKey').checked = false; refresh(); f(prefix+'apiKey').focus() })
    pane.querySelector(`[data-remove-key="${prefix}"]`).addEventListener('click', () => { f(prefix+'apiKey').value = ''; f(prefix+'clearKey').checked = true; replacing.delete(keyID(prefix)); invalidate(prefix); syncSource(); markDirty(); refresh() })
    pane.querySelector(`[data-cancel-key="${prefix}"]`).addEventListener('click', () => { f(prefix+'apiKey').value = ''; f(prefix+'clearKey').checked = false; replacing.delete(keyID(prefix)); invalidate(prefix); syncSource(); markDirty(); refresh() })
    pane.querySelector(`[data-endpoint="${prefix}"]`).addEventListener('toggle', e => { if (e.target.classList.contains('required') && !e.target.open) e.target.open = true })
  }
  pane.querySelector('[data-add-source]').addEventListener('click', () => { syncSource(); const id = 'source_' + Array.from(crypto.getRandomValues(new Uint8Array(6)),b => b.toString(16).padStart(2,'0')).join(''); sources.push({id,name:'New connection',provider:'openai',baseURL:defaults.openai,apiKey:'',model:''}); editSource(id); markDirty(); f('sourceName').focus() })
  pane.querySelector('[data-default-source]').addEventListener('click', () => { syncSource(); defaultID = selectedID; f('reindex').checked = false; invalidate('embed-'); drawSources(); markDirty(); refresh() })
  pane.querySelector('[data-remove-source]').addEventListener('click', async () => {
    if (sources.length === 1) return
    syncSource(); const removing = source(), replacement = sources.find(s => s.id !== selectedID), usesEmbedding = f('embeddingMode').value === 'source' && f('embeddingSource').value === selectedID
    if (usesEmbedding) {
      status.textContent = 'Choose another document-search connection or disable document search before removing this connection.'; status.classList.add('error'); pane.querySelector('[data-embedding-section]').open = true; return
    }
    if (saved.sources.some(s => s.id === selectedID) && !await confirm(`Remove “${removing.name || removing.id}” when you save?` + (selectedID === defaultID ? ` “${replacement.name || replacement.id}” will become the default.` : '') + (usesEmbedding ? ' Document search will use the default connection; confirm any required index rebuild before saving.' : ''))) return
    const id = selectedID; sources = sources.filter(s => s.id !== id); if (defaultID === id) defaultID = replacement.id
    if (usesEmbedding) f('embeddingMode').value = 'default'
    catalogs.delete(id); checks.delete(id); editSource(defaultID); markDirty(); refresh()
  })
  f('embeddingMode').addEventListener('change', () => { f('reindex').checked = false; invalidate('embed-'); refresh() })
  f('embeddingSource').addEventListener('change', () => { f('reindex').checked = false; invalidate('embed-'); refresh() })
  form.addEventListener('change', () => { if (!busy && !resetPending) { syncSource(); markDirty(); drawSources(); refresh() } })
  form.addEventListener('input', () => { if (!busy && !resetPending) { syncSource(); markDirty(); refresh() } })
  form.addEventListener('submit', e => { e.preventDefault(); void perform('save') })
  pane.querySelector('[data-discard]').addEventListener('click', async () => { setBusy(true); try { await load(); status.textContent = '' } catch (error) { status.textContent = error.message; status.classList.add('error') } finally { setBusy(false) } })
  pane.querySelectorAll('[data-action]').forEach(button => button.addEventListener('click', () => { void perform(button.dataset.action) }))
  try {
    if (!options.admin) { const me = await fetch('/api/me').then(r => r.json()); if (me.multiUser) { form.remove(); pane.querySelector('[data-source]').textContent = 'AI providers are configured by the global administrator in Admin → AI provider.'; return } }
    await load(); form.hidden = false; status.textContent = ''
  } catch (error) { const el = pane.querySelector('[data-load-error]'); el.hidden = false; el.textContent = error.message; form.querySelectorAll('button').forEach(button => { button.disabled = true }) }
}
