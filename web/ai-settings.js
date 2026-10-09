// One form for the local owner and the global admin. Never persist keys in the browser.
async function renderAITab(container, options = {}) {
  container.replaceChildren()
  const page = document.createElement('div')
  page.className = options.admin ? 'ai-settings-page' : 'settings-page ai-settings-page'
  const pane = document.createElement('div')
  pane.className = 'ai-settings'
  page.appendChild(pane); container.appendChild(page)
  const providers = '<option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="ollama">Ollama</option><option value="other">Other compatible</option>'
  const connection = prefix => `
    <label class="ai-field">Provider<select name="${prefix}provider">${providers}</select></label>
    <p class="ai-hint" data-hint="${prefix}"></p>
    <label class="ai-field" data-url="${prefix}">Server URL<input name="${prefix}baseURL" type="url" spellcheck="false" placeholder="http://localhost:11434"></label>
    <label class="ai-field" data-key="${prefix}">API key<input name="${prefix}apiKey" type="password" autocomplete="new-password" placeholder="Enter your API key"></label>
    <div class="ai-row ai-clear-key" data-clear="${prefix}"><div><div class="ai-row-title">Remove saved key</div><div class="ai-hint">The next save will delete this credential.</div></div><label class="toggle-switch"><input type="checkbox" name="${prefix}clearKey" aria-label="Remove saved key"><span class="toggle-track"></span></label></div>`
  const model = (prefix, action) => `
    <div class="ai-actions"><button class="ai-button primary" type="button" data-action="${action}models">Connect</button><span class="ai-connect-status" data-connected="${prefix}" role="status"></span></div>
    <label class="ai-field" data-model-picker="${prefix}" hidden>Choose a model<select name="${prefix}modelChoice"><option value="">Connect to see models</option></select></label>
    <p class="ai-hint" data-model-help="${prefix}" hidden>Choosing a model saves your changes automatically${prefix ? ' and updates document search' : '. New conversations use the default source'}.</p>
    <details class="ai-advanced"><summary>Model not listed? Enter its ID</summary>
      <label class="ai-field">Model ID<input name="${prefix}model" list="${options.admin ? 'admin-' : ''}ai-${prefix}models" placeholder="Exact model ID" spellcheck="false"><datalist id="${options.admin ? 'admin-' : ''}ai-${prefix}models"></datalist></label>
      <div class="ai-actions"><button class="ai-button" type="button" data-action="${prefix ? 'save' : 'chat'}">Save model ID</button><button class="ai-button" type="button" data-action="${action}test">Test ${prefix ? 'embeddings' : 'model'}</button></div>
    </details>`
  pane.innerHTML = `
    <div class="ai-header">
      <div class="ai-title-line">${options.admin ? '<h2 class="ai-admin-title">AI provider</h2>' : '<h2 class="ai-settings-title">AI provider</h2>'}<span class="ai-scope">${options.admin ? 'Deployment' : 'Personal'}</span></div>
      <p class="ai-description" data-source>Loading configuration…</p>
      <div class="ai-current" aria-live="polite"><span>Default model</span><strong data-current-model>Not configured</strong><small data-current-source></small></div>
    </div>
    <form autocomplete="off">
      <div class="ai-save-state" data-save-state role="status">Loading…</div>
      <section class="ai-section">
        <div class="config-cat-header"><span class="config-cat-name">Conversation models</span></div>
        <p class="ai-hint ai-section-hint">Connect a provider, then choose a model. Add more sources if needed.</p>
        <div class="ai-source-toolbar"><div class="ai-sources" data-sources aria-label="AI sources"></div><button class="ai-button" type="button" data-add-source>+ Add source</button></div>
        <div class="ai-source-editor">
          <div class="ai-editor-heading"><strong data-editor-title>Source</strong><button class="ai-button danger" type="button" data-remove-source>Remove source</button></div>
          <label class="ai-field">Source name<input name="sourceName" maxlength="100" placeholder="e.g. Local Ollama"></label>
          <div class="ai-subsection">
            ${connection('')}${model('', '')}
            <div class="ai-model-settings-row"><span class="ai-hint" data-model-capabilities>Context: automatic</span><button type="button" class="ai-button" data-model-settings>Model settings…</button></div>
          </div>
          <div class="ai-default-action"><span data-default-action-help>Make this model the default for new conversations.</span><button class="ai-button" type="button" data-default-source>Use as default</button></div>
        </div>
        <input name="chatVision" type="checkbox" hidden aria-label="Legacy vision fallback">
      </section>
      <section class="ai-section">
        <div class="config-cat-header"><span class="config-cat-name">Document search</span></div>
        <p class="ai-hint ai-section-hint">Optional. An embedding model lets the agent search indexed documents.</p>
        <div class="ai-row"><div><div class="ai-row-title">Use same provider</div><div class="ai-hint">Reuse the default conversation source and its credential.</div></div><label class="toggle-switch"><input name="useSameProvider" type="checkbox" checked aria-label="Use same provider"><span class="toggle-track"></span></label></div>
        <div class="ai-embedding-config"><label class="ai-field" data-embedding-source hidden>Embedding source<select name="embeddingSource"></select></label>
          <div data-embedding-connection hidden>${connection('embed-')}</div>
          <p class="ai-hint" data-unsupported hidden>Anthropic has no embedding endpoint. Select another source or a dedicated provider.</p>
          ${model('embed-', 'embedding_')}
          <p class="ai-hint">Leave the embedding model empty to disable document search.</p>
        </div>
        <div class="ai-row" data-reindex hidden><div><div class="ai-row-title">Rebuild the document index when saving</div><div class="ai-hint">Indexed text is sent to the selected embedding provider; API charges may apply.</div></div><label class="toggle-switch"><input name="reindex" type="checkbox" aria-label="Rebuild the document index when saving"><span class="toggle-track"></span></label></div>
        <p class="ai-hint" data-embedding-status role="status" aria-live="polite"></p>
      </section>
      <div class="ai-footer">
        <div class="ai-actions"><button class="ai-button primary" type="submit" data-save>Save other changes</button><button class="ai-button" type="button" data-action="reset" hidden>Use server settings</button></div>
        <p class="ai-hint">Choosing a model saves automatically. Use Save other changes for names and embedding options. Keys are stored encrypted and never displayed.</p>
      </div>
    </form>
    <p class="ai-status" data-status role="status" aria-live="polite"></p>
    <dialog class="ai-model-dialog" aria-labelledby="ai-model-title">
      <form data-model-form>
        <h3 id="ai-model-title">Model settings</h3>
        <p class="ai-hint" data-model-source></p>
        <label class="ai-field">Model<input name="settingsModel" list="ai-settings-models" required maxlength="200" spellcheck="false"><datalist id="ai-settings-models"></datalist></label>
        <label class="ai-field">Context window<select name="contextMode"><option value="auto">Automatic</option><option value="manual">Set token limit</option></select></label>
        <label class="ai-field" data-context-limit hidden>Context window (tokens)<input name="contextWindow" type="number" min="4096" max="2000000" step="1" placeholder="e.g. 500000"></label>
        <p class="ai-hint" data-context-detected></p>
        <label class="ai-field">Vision support<select name="modelVision"><option value="auto">Automatic</option><option value="yes">Supports images</option><option value="no">Text only</option></select></label>
        <p class="ai-hint" data-vision-detected></p>
        <p class="ai-hint">Prism manages conversation compaction automatically. These settings do not change your default model.</p>
        <p class="ai-status" data-model-status role="status" aria-live="polite"></p>
        <div class="ai-model-footer"><button class="ai-button" type="button" data-model-reset>Reset to automatic</button><div><button class="ai-button" type="button" data-model-close>Close</button><button class="ai-button primary" type="submit">Save</button></div></div>
      </form>
    </dialog>`
  const form = pane.querySelector('form'), status = pane.querySelector('[data-status]')
  form.hidden=true
  const f = name => form.elements.namedItem(name)
  let saved = null, busy = false, sources = [], selectedID = null, defaultID = null, dirty = false
  const catalogs = new Map()
  const modelInfo = new Map()
  const warnOnLeave=e=>{if(pane.isConnected&&(dirty||busy)){e.preventDefault();e.returnValue=''}}
  const warnOnNavigate=e=>{if(!pane.isConnected||(!dirty&&!busy)||!e.target.closest('a[href],.nav-item')||pane.contains(e.target))return;if(!window.confirm(busy?'Configuration is still being saved or checked. Leave this page?':'Your changes have not been saved. Leave this page?')){e.preventDefault();e.stopImmediatePropagation()}}
  window.addEventListener('beforeunload',warnOnLeave)
  document.addEventListener('click',warnOnNavigate,true)
  const cleanup=new MutationObserver(()=>{if(!pane.isConnected){window.removeEventListener('beforeunload',warnOnLeave);document.removeEventListener('click',warnOnNavigate,true);cleanup.disconnect()}})
  cleanup.observe(container,{childList:true})
  function markDirty() { dirty=true; showSaveState() }
  function showSaveState() {
    const el=pane.querySelector("[data-save-state]")
    el.textContent=busy ? "Working…" : dirty ? "Unsaved changes — click Save other changes." : (saved?.model ? "All changes saved." : "Connect a provider and choose a model to get started.")
    el.classList.toggle("pending",dirty)
    const primary=sources.find(s=>s.id===defaultID)
    pane.querySelector('[data-current-model]').textContent=saved?.model || 'Not configured'
    pane.querySelector('[data-current-source]').textContent=primary?.name || ''
    pane.querySelector('[data-save]').hidden=!dirty || busy
  }
  function catalogKey(prefix) { return prefix ? "embedding" : selectedID }
  function showModels(prefix) {
    const choice=f(prefix+"modelChoice"),models=catalogs.get(catalogKey(prefix))
    choice.replaceChildren()
    const hint=document.createElement("option");hint.value="";hint.textContent="Choose a model…";choice.append(hint)
    const current=f(prefix+"model").value
    for(const name of new Set([...(models||[]),...(current?[current]:[])])) {const opt=document.createElement("option");opt.value=name;opt.textContent=name;choice.append(opt)}
    choice.value=current
    pane.querySelector(`[data-model-picker="${prefix}"]`).hidden=!models&&!current
    pane.querySelector(`[data-model-help="${prefix}"]`).hidden=!models&&!current
  }
  function invalidate(prefix) {
    catalogs.delete(catalogKey(prefix));showModels(prefix)
    pane.querySelector(`[data-connected="${prefix}"]`).textContent="Connection changed — click Connect."
  }
  const defaults = {openai:'https://api.openai.com/v1',anthropic:'https://api.anthropic.com',ollama:'',other:''}
  const identity = e => JSON.stringify([e.provider === 'other' ? 'openai' : e.provider, e.baseURL.replace(/\/+$/, ''), e.model.trim()])
  function syncSource() {
    const src=sources.find(s=>s.id===selectedID)
    if(!src) return
    Object.assign(src,{name:f('sourceName').value,provider:f('provider').value,baseURL:f('baseURL').value,apiKey:f('apiKey').value,clearKey:f('clearKey').checked,model:f('model').value})
  }
  function editSource(id) {
    selectedID=id
    const src=sources.find(s=>s.id===id)
    for(const k of ['provider','baseURL','apiKey','model']) f(k).value=src[k] || ''
    f('sourceName').value=src.name || '';f('clearKey').checked=!!src.clearKey
    pane.querySelector('datalist').replaceChildren()
    showModels('');pane.querySelector('[data-connected=""]').textContent=''
    drawSources();refresh()
  }
  function drawSources() {
    const list=pane.querySelector('[data-sources]');list.replaceChildren()
    for(const src of sources) {
      const row=document.createElement('button');row.type='button';row.className='ai-source'+(src.id===selectedID?' selected':'');row.setAttribute('aria-pressed',String(src.id===selectedID))
      const title=document.createElement('span');title.className='ai-source-title'
      const name=document.createElement('strong');name.textContent=src.name || src.id;title.append(name)
      if(src.id===defaultID){const badge=document.createElement('em');badge.className='ai-default-badge';badge.textContent='Default';title.append(badge)}
      const detail=document.createElement('small');detail.textContent=src.provider+(src.model ? ' · '+src.model : ' · No model yet')
      row.append(title,detail);row.addEventListener('click',()=>{syncSource();editSource(src.id)})
      row.disabled=busy;list.append(row)
    }
    const choice=f('embeddingSource'),previous=choice.value
    choice.replaceChildren()
    for(const src of [{id:'',name:'Dedicated connection'},...sources]) {const opt=document.createElement('option');opt.value=src.id;opt.textContent=src.name||src.id;choice.append(opt)}
    choice.value=previous
  }
  function payload(action) {
    syncSource()
    const primary=sources.find(s=>s.id===defaultID) || sources[0]
    const probing=action==='models'||action==='test'
    return {action,sources:sources.filter(s=>!probing || s.id===selectedID).map(s=>({...s})),defaultSource:probing?selectedID:defaultID,sourceID:probing?selectedID:undefined,
      provider:primary.provider,baseURL:primary.baseURL,apiKey:primary.apiKey,model:primary.model,chatVision:f('chatVision').checked,
      embedding:{useSameProvider:f('useSameProvider').checked,sourceID:f('embeddingSource').value,provider:f('embed-provider').value,baseURL:f('embed-baseURL').value,apiKey:f('embed-apiKey').value,clearKey:f('embed-clearKey').checked,model:f('embed-model').value,reindex:f('reindex').checked}}
  }
  function effective() {
    const p=payload('save'),e=p.embedding
    if(e.useSameProvider) return {...e,provider:p.provider,baseURL:p.baseURL}
    const src=sources.find(s=>s.id===e.sourceID)
    return src ? {...e,provider:src.provider,baseURL:src.baseURL} : e
  }
  function modelTokenLabel(value) {
    const n=Number(value)
    if(n>=1000000 && n%1000000===0)return `${n/1000000}M`
    if(n>=1000 && n%1000===0)return `${n/1000}k`
    return n.toLocaleString('en-US')
  }
  function modelInfoKey(src, model) { return JSON.stringify([src.id,src.provider,src.baseURL,model]) }
  async function fetchModelInfo(sourceID, model) {
    const r=await fetchConfig('/api/ai/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'model_info',sourceID,model})})
    const data=await r.json()
    if(!r.ok)throw new Error(data.error || 'Cannot read model capabilities')
    return data
  }
  function showModelCapabilities(selected, stored) {
    const el=pane.querySelector('[data-model-capabilities]'), model=f('model').value
    if(!stored || !model || stored.provider!==selected?.provider || stored.baseURL!==selected?.baseURL) {
      el.textContent='Choose and save a model to see its capabilities.'
      return
    }
    const key=modelInfoKey(stored,model)
    let entry=modelInfo.get(key)
    if(!entry) {
      entry={loading:true};modelInfo.set(key,entry)
      fetchModelInfo(stored.id,model).then(data=>{entry.data=data}).catch(()=>{entry.failed=true}).finally(()=>{
        entry.loading=false
        if(pane.isConnected && modelInfo.get(key)===entry)refresh()
      })
    }
    const cfg=selected?.modelSettings?.[model] || {}, detected=entry.data?.detected || {}
    const context=cfg.contextWindow ? `${modelTokenLabel(cfg.contextWindow)} tokens (manual)` : detected.contextWindow ? `${modelTokenLabel(detected.contextWindow)} tokens (auto-detected)` : entry.loading ? 'checking…' : 'not detected (conservative limit)'
    const vision=typeof cfg.vision==='boolean' ? `${cfg.vision?'supported':'text only'} (manual)` : typeof detected.vision==='boolean' ? `${detected.vision?'supported':'text only'} (auto-detected)` : entry.loading ? 'checking…' : 'not detected (existing setting)'
    el.textContent=`Context: ${context} · Vision: ${vision}`
  }
  function refresh() {
    for (const prefix of ['', 'embed-']) {
      const provider = f(prefix+'provider').value, custom = provider === 'ollama' || provider === 'other' || (f(prefix+'baseURL').value && f(prefix+'baseURL').value !== defaults[provider])
      pane.querySelector(`[data-url="${prefix}"]`).hidden = !custom
      pane.querySelector(`[data-key="${prefix}"]`).hidden = provider === 'ollama'
      pane.querySelector(`[data-hint="${prefix}"]`).textContent = provider === 'other' ? 'OpenAI-compatible API: vLLM, SGLang or llama.cpp. Include /v1 in the URL.' : provider === 'ollama' ? 'Enter the URL reachable from the Prism server.' : defaults[provider]
      f(prefix+'baseURL').placeholder = provider === 'other' ? 'http://your-server:8000/v1' : 'http://your-server:11434'
      const prior = prefix ? saved?.embedding : saved?.sources?.find(s=>s.id===selectedID)
      const same = prior?.provider === provider && prior?.baseURL === f(prefix+'baseURL').value.replace(/\/+$/, '')
      f(prefix+'apiKey').placeholder = same && prior?.keyConfigured ? 'Saved securely — leave blank to keep' : 'Enter your API key'
      pane.querySelector(`[data-clear="${prefix}"]`).hidden = !(same && prior?.keyConfigured) || provider === 'ollama'
    }
    pane.querySelector('[data-embedding-source]').hidden=f('useSameProvider').checked
    pane.querySelector('[data-embedding-connection]').hidden = f('useSameProvider').checked || !!f('embeddingSource').value
    const selected=sources.find(s=>s.id===selectedID)
    const stored=saved?.sources?.find(s=>s.id===selectedID)
    pane.querySelector('[data-model-settings]').disabled=busy || !stored?.model || stored.baseURL!==selected?.baseURL || stored.provider!==selected?.provider
    showModelCapabilities(selected,stored)
    pane.querySelector('[data-editor-title]').textContent=selected?.name || 'New source'
    const defaultAction=pane.querySelector('.ai-default-action')
    defaultAction.hidden=selectedID===defaultID || !selected?.model
    pane.querySelector('[data-default-source]').disabled=busy
    pane.querySelector('[data-default-action-help]').textContent=selected?.model ? 'Make this model the default for new conversations.' : 'Choose and save a model first.'
    pane.querySelector('[data-remove-source]').hidden=sources.length===1
    pane.querySelector('[data-remove-source]').disabled=busy || sources.length===1
    const unsupported = effective().provider === 'anthropic'
    pane.querySelector('[data-unsupported]').hidden = !unsupported
    pane.querySelectorAll('[data-action^="embedding_"]').forEach(b => b.disabled = busy || unsupported)
    pane.querySelector('[data-reindex]').hidden = !saved || (identity(effective()) === identity(saved.embedding) && !saved.embeddingStatus?.includes("failed"))
  }
  // Bound both the headers and response body; a proxy/provider stall must
  // never leave every settings control disabled forever.
  async function fetchConfig(url='/api/ai/config', options={}) {
    const controller=new AbortController()
    const timer=setTimeout(()=>controller.abort(),40000)
    try {
      const response=await fetch(url,{...options,signal:controller.signal})
      const text=await response.text()
      return {ok:response.ok,status:response.status,text:async()=>text,json:async()=>JSON.parse(text)}
    } catch(err) {
      if(controller.signal.aborted){const timeout=new Error('The server did not respond in time. Reopen settings to check whether your changes were saved before trying again.');timeout.name='TimeoutError';throw timeout}
      throw err
    } finally {clearTimeout(timer)}
  }
  async function request(action) {
    const r = await fetchConfig('/api/ai/config', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload(action))})
    const text = await r.text();let data
    try { data=JSON.parse(text) } catch { data={} }
    if (!r.ok) throw new Error(data.error || text || 'Request failed')
    return data
  }
  let monitorGeneration = 0
  function showEmbeddingStatus(data) {
    pane.querySelector('[data-embedding-status]').textContent = data.embeddingApplying
      ? 'Applying embeddings — '+data.embeddingStatus+'. Document search is temporarily unavailable.'
      : 'Embeddings: '+(data.embeddingStatus || 'not configured')+(data.embeddingPending ? ' Saved configuration is not active yet.' : '')
    saved.embeddingStatus=data.embeddingStatus
    saved.embeddingPending=data.embeddingPending
    refresh()
  }
  async function monitorEmbedding(generation) {
    while (pane.isConnected && generation===monitorGeneration) {
      await new Promise(resolve=>setTimeout(resolve,1000))
      if (!pane.isConnected || generation!==monitorGeneration) return
      try {
        const r=await fetchConfig('/api/ai/config',{cache:'no-store'})
        if(!r.ok) throw new Error('Status unavailable')
        const data=await r.json()
        if(generation!==monitorGeneration || !pane.isConnected) return
        showEmbeddingStatus(data)
        if(!data.embeddingApplying) return
      } catch {
        if(generation===monitorGeneration && pane.isConnected) pane.querySelector('[data-embedding-status]').textContent='Cannot refresh embedding status. Reopen settings to check.'
        return
      }
    }
  }
  async function load() {
    const r=await fetchConfig('/api/ai/config',{cache:'no-store'})
    if (!r.ok) throw new Error('AI configuration is unavailable. In multi-user mode, use Admin → AI provider.')
    saved=await r.json()
    modelInfo.clear()
    sources=saved.sources.map(s=>({...s,apiKey:'',clearKey:false}));defaultID=saved.defaultSource
    selectedID=sources.some(s=>s.id===selectedID) ? selectedID : defaultID
    for (const key of ['provider','baseURL','model']) f('embed-'+key).value=saved.embedding[key] || (key==='provider' ? 'openai' : '')
    f('embed-apiKey').value='';f('embed-clearKey').checked=false
    editSource(selectedID)
    f('embeddingSource').value=saved.embedding.sourceID || ''
    f('chatVision').checked=!!saved.chatVision
    f('useSameProvider').checked=saved.embedding.useSameProvider !== false
    f('reindex').checked=false
    pane.querySelector('[data-action="reset"]').hidden=saved.serverDefaultsAvailable===false
    pane.querySelector('[data-source]').textContent=(options.admin ? 'Deployment configuration — applies to all users and shared agents. ' : 'Configure the AI used by your agent and apps. ')+(saved.source==='server' && saved.serverDefaultsAvailable!==false ? 'Using server defaults.' : '')
    showEmbeddingStatus(saved)
    const generation=++monitorGeneration
    if(saved.embeddingApplying) void monitorEmbedding(generation)
    showModels('embed-')
    dirty=false;showSaveState();refresh()
  }
  async function perform(action, quiet=false) {
    if (busy) return
    if (action==='reset') {
      // Prism's shared confirmation dialog, with native confirm only as a fallback.
      const message='Restore the server AI settings? If the embedding provider or model changes, the document index will be rebuilt automatically.'
      const ok=typeof PrismModal!=='undefined' ? await PrismModal.confirm(message) : window.confirm(message)
      if (!ok) return
    }
    busy=true;pane.classList.add('is-busy');const controls=[...form.querySelectorAll('input,select,button')]
    controls.forEach(el=>el.disabled=true);status.classList.remove('error')
    const connectionPrefix=action==='embedding_models'?'embed-':''
    const localStatus=action.endsWith('models')?pane.querySelector(`[data-connected="${connectionPrefix}"]`):null
    if(localStatus)localStatus.textContent='Connecting…'
    showSaveState()
    if (!quiet) status.textContent=action.endsWith('models') ? 'Connecting to the provider…' : action.includes('test') ? 'Testing…' : 'Saving changes…'
    let persisted=false
    try {
      let result
      if (action==='reset') {
        const r=await fetchConfig('/api/ai/config?reindex=true',{method:'DELETE'});result=await r.json();if(!r.ok) throw new Error(result.error || 'Cannot restore server settings')
      } else result=await request(action)
      if (action.endsWith('models')) {
        const list=pane.querySelectorAll('datalist')[action==='models' ? 0 : 1];list.replaceChildren()
        for(const name of result.models || []) {const o=document.createElement('option');o.value=name;list.appendChild(o)}
        catalogs.set(catalogKey(connectionPrefix),result.models||[]);showModels(connectionPrefix)
        localStatus.textContent=result.models?.length ? `Connected · ${result.models.length} models` : 'Connected, but no models were returned. Enter a model ID below.'
        if (!quiet) status.textContent='Connection ready. Choose a model to save it automatically.'
      } else if (action==='test') status.textContent='The model responded. This text test does not verify vision support.'
      else if(action==='embedding_test') status.textContent=`Embedding endpoint verified · ${result.dimension} dimensions.`
      else {persisted=true;status.textContent='Saved. Refreshing…';await load();status.textContent='Changes saved. New conversations use the selected default model.'}
      return true
    } catch(err) {if(persisted){dirty=false;saved.model=payload('save').model}status.textContent=(persisted?'Saved, but unable to refresh the page. ' : action==='save'&&err.name!=='TimeoutError'?'Not saved: ':'')+err.message;status.classList.add('error');if(localStatus)localStatus.textContent='Connection failed: '+err.message;return persisted}
    finally {busy=false;pane.classList.remove('is-busy');controls.forEach(el=>el.disabled=false);pane.querySelectorAll('[data-sources] button').forEach(el=>el.disabled=false);showSaveState();refresh()}
  }
  const dialog=pane.querySelector('dialog'), modelForm=dialog.querySelector('form')
  const mf=name=>modelForm.elements.namedItem(name)
  const modelStatus=dialog.querySelector('[data-model-status]')
  let modelSourceID=null, modelGeneration=0, modelWorking=false
  function modelControls(working) {
    modelWorking=working
    modelForm.querySelectorAll('input,select,button').forEach(el=>el.disabled=working && !el.hasAttribute('data-model-close'))
  }
  function contextFields() {
    const manual=mf('contextMode').value==='manual'
    dialog.querySelector('[data-context-limit]').hidden=!manual
    mf('contextWindow').required=manual
  }
  async function modelRequest(action, settings) {
    const r=await fetchConfig('/api/ai/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action,sourceID:modelSourceID,model:mf('settingsModel').value.trim(),settings})})
    const data=await r.json()
    if(!r.ok)throw new Error(data.error || 'Cannot update model settings')
    return data
  }
  async function readModelInfo() {
    const generation=++modelGeneration
    modelControls(true);modelStatus.classList.remove('error');modelStatus.textContent='Checking model capabilities…'
    try {
      const id=mf('settingsModel').value.trim()
      const data=await fetchModelInfo(modelSourceID,id)
      if(generation!==modelGeneration || !dialog.open)return
      const src=saved.sources.find(s=>s.id===modelSourceID)
      if(src){modelInfo.set(modelInfoKey(src,id),{data,loading:false});refresh()}
      mf('contextMode').value=data.settings?.contextWindow?'manual':'auto'
      mf('contextWindow').value=data.settings?.contextWindow || ''
      mf('modelVision').value=data.settings?.vision===true?'yes':data.settings?.vision===false?'no':'auto'
      const detected=data.detected?.contextWindow
      mf('contextMode').options[0].textContent=detected ? `Automatic — ${modelTokenLabel(detected)} tokens` : 'Automatic — not detected'
      mf('modelVision').options[0].textContent=typeof data.detected?.vision==='boolean' ? `Automatic — ${data.detected.vision?'supported':'text only'}` : 'Automatic — existing setting'
      dialog.querySelector('[data-vision-detected]').textContent=data.detected?.vision===true?'Provider reports image support.':data.detected?.vision===false?'Provider reports a text-only model.':'Image support was not reported. Automatic keeps your existing configuration.'
      dialog.querySelector('[data-context-detected]').textContent=detected ? `Automatic limit: ${Number(detected).toLocaleString('en-US')} tokens, reported by the provider. For Ollama, this respects its configured context.` : 'The provider did not report a context window. Prism uses a conservative limit; enter the server’s effective token limit if known.'
      modelStatus.textContent=''
      contextFields()
    } catch(err) {if(generation===modelGeneration){modelStatus.textContent=err.message;modelStatus.classList.add('error')}}
    finally {if(generation===modelGeneration)modelControls(false)}
  }
  pane.querySelector('[data-model-settings]').addEventListener('click',()=>{
    modelSourceID=selectedID
    const src=saved.sources.find(s=>s.id===modelSourceID)
    dialog.querySelector('[data-model-source]').textContent=src.name+' · '+src.provider
    const list=dialog.querySelector('datalist');list.replaceChildren()
    for(const id of new Set([src.model,...(catalogs.get(modelSourceID)||[]),...Object.keys(src.modelSettings||{})])) {
      const o=document.createElement('option');o.value=id;list.append(o)
    }
    mf('settingsModel').value=src.model
    dialog.showModal();void readModelInfo()
  })
  mf('settingsModel').addEventListener('change',()=>{if(mf('settingsModel').value.trim())void readModelInfo()})
  mf('contextMode').addEventListener('change',contextFields)
  dialog.querySelector('[data-model-reset]').addEventListener('click',()=>{
    mf('contextMode').value='auto';mf('contextWindow').value='';mf('modelVision').value='auto';contextFields()
    modelStatus.textContent='Click Save to restore automatic settings for this model.'
  })
  dialog.querySelector('[data-model-close]').addEventListener('click',()=>dialog.close())
  dialog.addEventListener('cancel',e=>{if(modelWorking){e.preventDefault();return}})
  dialog.addEventListener('close',()=>{modelGeneration++;modelControls(false)})
  modelForm.addEventListener('submit',async e=>{
    e.preventDefault();if(modelWorking || !modelForm.reportValidity())return
    const settings={contextWindow:mf('contextMode').value==='manual'?Number(mf('contextWindow').value):0}
    if(mf('modelVision').value!=='auto')settings.vision=mf('modelVision').value==='yes'
    const sourceID=modelSourceID, id=mf('settingsModel').value.trim(), generation=++modelGeneration
    modelControls(true);modelStatus.classList.remove('error');modelStatus.textContent='Saving…'
    try {
      await modelRequest('model_settings',settings)
      for(const list of [sources,saved.sources]) {
        const src=list.find(s=>s.id===sourceID);if(!src)continue
        src.modelSettings={...(src.modelSettings||{})}
        if(!settings.contextWindow && settings.vision===undefined)delete src.modelSettings[id]
        else src.modelSettings[id]={...settings}
      }
      refresh()
      if(generation===modelGeneration)modelStatus.textContent='Saved. Applied to the next message using this model.'
    }catch(err){if(generation===modelGeneration){modelStatus.textContent=err.message;modelStatus.classList.add('error')}}
    finally {if(generation===modelGeneration)modelControls(false)}
  })
  for(const prefix of ['', 'embed-']) {
    f(prefix+'provider').addEventListener('change',()=>{
      f(prefix+'baseURL').value=defaults[f(prefix+'provider').value];f(prefix+'apiKey').value='';f(prefix+'model').value='';f(prefix+'clearKey').checked=false
      pane.querySelectorAll('datalist')[prefix ? 1 : 0].replaceChildren();f('reindex').checked=false;invalidate(prefix);refresh()
    })
    for(const key of ['baseURL','apiKey','clearKey']) f(prefix+key).addEventListener('input',()=>invalidate(prefix))
    f(prefix+'modelChoice').addEventListener('change',async e=>{if(!e.target.value)return;f(prefix+'model').value=e.target.value;if(!prefix&&!sources.find(s=>s.id===defaultID)?.model)defaultID=selectedID;markDirty();await perform(prefix ? 'save' : 'chat')})
  }
  pane.querySelector('[data-add-source]').addEventListener('click',()=>{
    syncSource();const id='source_'+Array.from(crypto.getRandomValues(new Uint8Array(6)),b=>b.toString(16).padStart(2,'0')).join('')
    sources.push({id,name:'New source',provider:'openai',baseURL:defaults.openai,apiKey:'',model:''});editSource(id);markDirty();f('sourceName').focus()
  })
  pane.querySelector('[data-default-source]').addEventListener('click',async()=>{
    if (selectedID===defaultID) { status.textContent='This source is already the default.'; return }
    syncSource();defaultID=selectedID;f('reindex').checked=false;markDirty();catalogs.delete('embedding');showModels('embed-');drawSources();refresh();await perform('chat')
  })
  pane.querySelector('[data-remove-source]').addEventListener('click',async()=>{
    if(sources.length===1)return
    syncSource()
    const removing=sources.find(s=>s.id===selectedID), persisted=saved.sources.some(s=>s.id===selectedID)
    const replacement=sources.find(s=>s.id!==selectedID)
    const usesEmbedding=!f('useSameProvider').checked && f('embeddingSource').value===selectedID
    const message=`Remove “${removing.name||removing.id}”?`+(selectedID===defaultID ? ` “${replacement.name||replacement.id}” will become the default.` : '')+(usesEmbedding?' Embeddings will use the default source instead. An index rebuild may need confirmation.':'')
    if(persisted) {const ok=typeof PrismModal!=='undefined'?await PrismModal.confirm(message):window.confirm(message);if(!ok)return}
    const before=sources.map(s=>({...s})),oldDefault=defaultID,oldSelected=selectedID,oldEmbedding=f('embeddingSource').value,oldSame=f('useSameProvider').checked
    sources=sources.filter(s=>s.id!==selectedID)
    if(selectedID===defaultID)defaultID=replacement.id
    if(usesEmbedding){f('useSameProvider').checked=true;f('embeddingSource').value=''}
    catalogs.delete(selectedID);editSource(defaultID);markDirty()
    if(persisted) {
      if(await perform('save'))status.textContent='Source removed and saved.'
      else {sources=before;defaultID=oldDefault;editSource(oldSelected);f('embeddingSource').value=oldEmbedding;f('useSameProvider').checked=oldSame;refresh();status.textContent='Source was not removed. '+status.textContent}
    }else{status.textContent='Unsaved source discarded. Other changes have not been saved.'}
  })
  form.addEventListener('change',e=>{if(busy)return;markDirty();syncSource();drawSources();refresh()})
  form.addEventListener('input',()=>{markDirty();refresh()})
  form.addEventListener('submit',e=>{e.preventDefault();perform('save')})
  pane.querySelectorAll('[data-action]').forEach(b=>b.addEventListener('click',()=>perform(b.dataset.action)))
  try {
    if(!options.admin) {const me=await fetch('/api/me').then(r=>r.json());if(me.multiUser){form.remove()
    pane.querySelector('[data-source]').textContent='AI providers are configured by the global administrator in Admin → AI provider.';return}}
    await load()
    form.hidden=false
    status.textContent=''
  } catch(err) {status.textContent=err.message;status.classList.add('error');form.querySelectorAll('button').forEach(b=>b.disabled=true)}
}
