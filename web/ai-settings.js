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
    <label class="ai-check" data-clear="${prefix}"><input type="checkbox" name="${prefix}clearKey">Remove the saved key</label>`
  const model = (prefix, action) => `
    <label class="ai-field">Model<input name="${prefix}model" list="${options.admin ? 'admin-' : ''}ai-${prefix}models" placeholder="Select or enter a model ID" spellcheck="false"><datalist id="${options.admin ? 'admin-' : ''}ai-${prefix}models"></datalist></label>
    <div class="ai-actions"><button class="ai-button" type="button" data-action="${action}models">Load models</button><button class="ai-button" type="button" data-action="${action}test">Test ${prefix ? 'embeddings' : 'model'}</button></div>`
  pane.innerHTML = `
    <div class="settings-page-title">AI provider</div>
    <p class="ai-hint" data-source>Loading configuration…</p>
    <form autocomplete="off">
      <section class="ai-section">
        <div class="config-cat-header"><span class="config-cat-icon">✦</span><span class="config-cat-name">AI sources</span></div>
        <div class="ai-sources" data-sources></div>
        <div class="ai-actions"><button class="ai-button" type="button" data-add-source>+ Add a source</button></div>
        <div class="ai-source-editor">
        <label class="ai-field">Source name<input name="sourceName" maxlength="100" placeholder="e.g. Local Ollama"></label>
        ${connection('')}${model('', '')}
        <div class="ai-actions"><button class="ai-button" type="button" data-default-source>Use as default</button><button class="ai-button" type="button" data-remove-source>Remove source</button></div></div>
        <p class="ai-hint">Each source keeps its own model and credential. The default model powers new conversations and app actions; all sources remain available in the chat model picker.</p>
        <div class="ai-row"><div><div class="ai-row-title">Default model supports vision</div><div class="ai-hint">Required to understand images and screenshots directly. Model catalogs do not always report this capability.</div></div><label class="toggle-switch"><input name="chatVision" type="checkbox" aria-label="Default model supports vision"><span class="toggle-track"></span></label></div>
        <p class="ai-hint">Vision is used for image attachments, screenshots and visual checks of widgets. A text-only model can still chat and use tools. Restart Prism to apply vision changes to the widget inspection fallback.</p>
      </section>
      <section class="ai-section">
        <div class="config-cat-header"><span class="config-cat-icon">⌕</span><span class="config-cat-name">Embeddings</span></div>
        <p class="ai-hint">Used to index and search documents. Choose an embedding model, separate from the conversation model.</p>
        <div class="ai-row"><div><div class="ai-row-title">Use same provider</div><div class="ai-hint">Reuse the default conversation source and its credential.</div></div><label class="toggle-switch"><input name="useSameProvider" type="checkbox" checked aria-label="Use same provider"><span class="toggle-track"></span></label></div>
        <label class="ai-field" data-embedding-source hidden>Embedding source<select name="embeddingSource"></select></label>
        <div data-embedding-connection hidden>${connection('embed-')}</div>
        <p class="ai-hint" data-unsupported hidden>Anthropic has no embedding endpoint. Uncheck “Use same provider” and select another provider to enable document search.</p>
        ${model('embed-', 'embedding_')}
        <p class="ai-hint">Suggested models come from the provider catalog. If yours is missing, enter its ID; “Test embeddings” verifies the actual endpoint. Leave empty to disable document search.</p>
        <label class="ai-check" data-reindex hidden><input name="reindex" type="checkbox">Rebuild the document index at the next server restart. All indexed text will be sent to the selected embedding provider; API charges may apply.</label>
      </section>
      <div class="ai-footer">
        <p class="ai-hint" data-restart hidden>Embedding changes are saved but require a server restart. Document search will be unavailable during the rebuild; the original index is retained if it fails. Stop other Prism servers using this database before restarting.</p>
        <div class="ai-actions"><button class="ai-button primary" type="submit">Save changes</button><button class="ai-button" type="button" data-action="reset">Use server settings</button></div>
        <p class="ai-hint">Chat changes apply from the next message. Tests send a small request and may incur API charges. Keys are stored encrypted and never displayed.</p>
      </div>
    </form>
    <p class="ai-status" data-status role="status" aria-live="polite"></p>`
  const form = pane.querySelector('form'), status = pane.querySelector('[data-status]')
  form.hidden=true
  const f = name => form.elements.namedItem(name)
  let saved = null, busy = false, sources = [], selectedID = null, defaultID = null
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
    drawSources();refresh()
  }
  function drawSources() {
    const list=pane.querySelector('[data-sources]');list.replaceChildren()
    for(const src of sources) {
      const row=document.createElement('button');row.type='button';row.className='ai-source'+(src.id===selectedID?' selected':'');row.setAttribute('aria-pressed',String(src.id===selectedID))
      const name=document.createElement('strong');name.textContent=src.name || src.id
      const detail=document.createElement('span');detail.textContent=src.provider+(src.model ? ' · '+src.model : '')+(src.id===defaultID ? ' · Default' : '')
      row.append(name,detail);row.addEventListener('click',()=>{syncSource();editSource(src.id)})
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
    pane.querySelector('[data-default-source]').disabled=busy || selectedID===defaultID
    pane.querySelector('[data-remove-source]').disabled=busy || sources.length===1
    const unsupported = effective().provider === 'anthropic'
    pane.querySelector('[data-unsupported]').hidden = !unsupported
    pane.querySelectorAll('[data-action^="embedding_"]').forEach(b => b.disabled = busy || unsupported)
    pane.querySelector('[data-reindex]').hidden = !saved || (identity(effective()) === identity(saved.embedding) && !saved.embeddingStatus?.includes("failed"))
  }
  async function request(action) {
    const r = await fetch('/api/ai/config', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload(action))})
    const text = await r.text();let data
    try { data=JSON.parse(text) } catch { data={} }
    if (!r.ok) throw new Error(data.error || text || 'Request failed')
    return data
  }
  async function load() {
    const r=await fetch('/api/ai/config',{cache:'no-store'})
    if (!r.ok) throw new Error('AI configuration is unavailable. In multi-user mode, use Admin → AI provider.')
    saved=await r.json()
    sources=saved.sources.map(s=>({...s,apiKey:'',clearKey:false}));defaultID=saved.defaultSource
    selectedID=sources.some(s=>s.id===selectedID) ? selectedID : defaultID
    for (const key of ['provider','baseURL','model']) f('embed-'+key).value=saved.embedding[key] || (key==='provider' ? 'openai' : '')
    f('embed-apiKey').value='';f('embed-clearKey').checked=false
    editSource(selectedID)
    f('embeddingSource').value=saved.embedding.sourceID || ''
    f('chatVision').checked=!!saved.chatVision
    f('useSameProvider').checked=saved.embedding.useSameProvider !== false
    f('reindex').checked=false
    pane.querySelector('[data-source]').textContent=(options.admin ? 'Deployment configuration — applies to all users and shared agents. ' : 'Configure the AI used by your agent and apps. ')+(saved.source==='server' ? 'Using server defaults.' : '')
    pane.querySelector('[data-restart]').hidden=!saved.embeddingRestartRequired
    refresh()
  }
  async function perform(action, quiet=false) {
    if (busy) return
    if (action==='reset') {
      // Prism's shared confirmation dialog, with native confirm only as a fallback.
      const message='Restore the server AI settings? If the embedding provider or model changes, the document index will be rebuilt at the next restart.'
      const ok=typeof PrismModal!=='undefined' ? await PrismModal.confirm(message) : window.confirm(message)
      if (!ok) return
    }
    busy=true;const controls=[...form.querySelectorAll('input,select,button')]
    controls.forEach(el=>el.disabled=true);status.classList.remove('error')
    if (!quiet) status.textContent=action.includes('test') ? 'Testing…' : 'Working…'
    try {
      let result
      if (action==='reset') {
        const r=await fetch('/api/ai/config?reindex=true',{method:'DELETE'});result=await r.json();if(!r.ok) throw new Error(result.error || 'Cannot restore server settings')
      } else result=await request(action)
      if (action.endsWith('models')) {
        const list=pane.querySelectorAll('datalist')[action==='models' ? 0 : 1];list.replaceChildren()
        for(const name of result.models || []) {const o=document.createElement('option');o.value=name;list.appendChild(o)}
        if (!quiet) status.textContent=`${result.models.length} models available. Select or enter a model ID.`
      } else if (action==='test') status.textContent='The model responded. This text test does not verify vision support.'
      else if(action==='embedding_test') status.textContent=`Embedding endpoint verified · ${result.dimension} dimensions.`
      else {await load();status.textContent='Saved. Chat applies from the next message.'+(saved.embeddingRestartRequired ? ' Restart the server to apply embeddings.' : '')}
    } catch(err) {status.textContent=err.message;status.classList.add('error')}
    finally {busy=false;controls.forEach(el=>el.disabled=false);pane.querySelectorAll('[data-sources] button').forEach(el=>el.disabled=false);refresh()}
  }
  for(const prefix of ['', 'embed-']) {
    f(prefix+'provider').addEventListener('change',()=>{
      f(prefix+'baseURL').value=defaults[f(prefix+'provider').value];f(prefix+'apiKey').value='';f(prefix+'model').value='';f(prefix+'clearKey').checked=false
      pane.querySelectorAll('datalist')[prefix ? 1 : 0].replaceChildren();f('reindex').checked=false;refresh()
    })
  }
  pane.querySelector('[data-add-source]').addEventListener('click',()=>{
    syncSource();const id='source_'+Array.from(crypto.getRandomValues(new Uint8Array(6)),b=>b.toString(16).padStart(2,'0')).join('')
    sources.push({id,name:'New source',provider:'openai',baseURL:defaults.openai,apiKey:'',model:''});editSource(id);f('sourceName').focus()
  })
  pane.querySelector('[data-default-source]').addEventListener('click',()=>{
    syncSource();defaultID=selectedID;f('reindex').checked=false;drawSources();refresh()
  })
  pane.querySelector('[data-remove-source]').addEventListener('click',()=>{
    if(selectedID===defaultID) {status.textContent='Choose another default source before removing this one.';return}
    if(!f('useSameProvider').checked && f('embeddingSource').value===selectedID) {status.textContent='Choose another embedding source before removing this one.';return}
    sources=sources.filter(s=>s.id!==selectedID);editSource(defaultID)
  })
  form.addEventListener('change',()=>{syncSource();drawSources();refresh()})
  form.addEventListener('input',refresh)
  form.addEventListener('submit',e=>{e.preventDefault();perform('save')})
  pane.querySelectorAll('[data-action]').forEach(b=>b.addEventListener('click',()=>perform(b.dataset.action)))
  try {
    if(!options.admin) {const me=await fetch('/api/me').then(r=>r.json());if(me.multiUser){form.remove();pane.querySelector('[data-source]').textContent='AI providers are configured by the global administrator in Admin → AI provider.';return}}
    await load()
    form.hidden=false
    if(sources.find(s=>s.id===selectedID)?.keyConfigured || f('provider').value==='ollama' || f('provider').value==='other') await perform('models',true)
    if(effective().provider!=='anthropic' && effective().baseURL) await perform('embedding_models',true)
  } catch(err) {status.textContent=err.message;status.classList.add('error');form.querySelectorAll('button').forEach(b=>b.disabled=true)}
}
