package server

// Shared settings form for the Telephony app — the switchboard persona and greeting, the
// reserved "voice" knowledge base, the spoken phrases and dictionary, and the SIP
// trunk. The editor is available before docking so administrators can connect
// PrismVox and inspect connection checks from the same settings page.
//
// Note where each field is stored: the persona lives in Prism (/api/voice), while
// the greeting, phrases, dictionary, handling parameters, voices and trunk are
// written straight through to Vox (/api/vox/...). Vox's own database is the store
// — this pane is an editor, not a second copy.

// adminTelephonyPane is the pane markup, inserted after Apps.
const adminTelephonyPane = `    <div class="pane" data-pane="telephony"><h2>Telephony</h2><span class="pane-scope">Deployment</span>
<style>.tel-section{margin:16px 0;border:1px solid var(--border);border-radius:8px}.tel-section>summary{padding:14px;cursor:pointer;font-weight:600}.tel-section-body{padding:0 14px 16px}.tel-section-body h3{font-size:15px;margin:20px 0 8px}.tel-tabs{display:flex;gap:8px;flex-wrap:wrap;margin:20px 0}.tel-tabs button[aria-selected=true]{background:var(--accent);color:var(--on-accent,#fff)}.tel-page[hidden],#tel-runtime[hidden]{display:none}.tel-health{display:flex;gap:8px;flex-wrap:wrap;margin:12px 0}.tel-health span{padding:5px 8px;border:1px solid var(--border);border-radius:6px;font-size:12px}.tel-connection-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.tel-connection-fields label{display:block}.tel-connection-fields input{width:100%}.tel-connection-fields input[type=checkbox]{width:auto}.tel-connection-fields .wide{grid-column:1/-1}@media(max-width:600px){.tel-connection-fields{grid-template-columns:1fr}}</style>
<p class="hint" id="tel-connection-summary">Connect PrismVox to configure your phone service and give your agent telephone capabilities.</p>
<nav class="tel-tabs" aria-label="Telephony settings"><button type="button" data-tel-tab="connection" aria-selected="true" onclick="showTelPage('connection')">Connection</button><button type="button" data-tel-tab="agent" aria-selected="false" onclick="showTelPage('agent')">Agent &amp; voice</button><button type="button" data-tel-tab="access" aria-selected="false" onclick="showTelPage('access')">Access &amp; resources</button><button type="button" data-tel-tab="advanced" aria-selected="false" onclick="showTelPage('advanced')">Advanced</button></nav>
<section class="tel-page" data-tel-page="connection">
<h3>Connect PrismVox</h3>
<form id="tel-connection-form" oninput="connectionDraftChanged()" onchange="connectionDraftChanged()" onsubmit="event.preventDefault();saveVoxConnection()">
<div class="tel-connection-fields">
<label class="wide">PrismVox API URL<input id="vox-url" type="url" placeholder="http://prismvox-ui:8000" autocomplete="url" required></label>
<label>Username<input id="vox-user" autocomplete="username"></label>
<label>Password<input id="vox-password" type="password" autocomplete="new-password" placeholder="Leave empty to keep the saved password"></label>
<label class="wide">Prism URL reachable from PrismVox<input id="vox-prism-url" type="url" placeholder="http://prism-server:8080" required></label>
<label class="wide">Public Prism URL (optional)<input id="vox-public-url" type="url" placeholder="https://prism.example.com"></label>
</div>
<div class="tel-health" id="vox-health" role="status"></div><p id="vox-connection-detail" class="hint" role="status"></p>
<div class="row"><button type="button" id="vox-test" onclick="testVoxConnection()">Test connection</button><button class="primary" id="vox-save" type="submit">Save &amp; connect</button><button type="button" id="vox-disconnect" onclick="disconnectVox()" hidden>Disconnect</button></div>
<p id="vox-connection-status" class="hint" role="status"></p>
</form>
<div data-tel-runtime-section hidden>      <h2 style="margin-top:26px;font-size:15px">SIP trunk</h2>
      <div class="hint" id="tel-sipstatus">Loading status…</div>
      <label>Registrar (host)</label><input id="sip-registrar" style="width:100%">
      <label>Registrar IP</label><input id="sip-registrar_ip" style="width:100%">
      <label>Allowed inbound addresses (optional)</label><input id="sip-registrar_match" placeholder="Default: registrar IP" style="width:100%">
      <label>Transport</label><select id="sip-transport_mode" onchange="updateTelSipTransport()" style="width:100%"><option value="tls">TLS + SRTP</option><option value="udp">UDP</option></select>
      <div class="row" style="gap:12px">
        <div style="flex:1"><label>SIP username</label><input id="sip-username" style="width:100%"></div>
        <div style="flex:1"><label>Password</label><input id="sip-password" type="password" placeholder="leave empty = unchanged" autocomplete="new-password" style="width:100%"></div>
      </div>
      <div class="row" style="gap:12px">
        <div style="flex:1"><label>SIP domain</label><input id="sip-domain" style="width:100%"></div>
        <div data-tel-transport="tls" style="flex:0 0 110px"><label>TLS port</label><input id="sip-tls_port" style="width:100%"></div>
        <div data-tel-transport="udp" style="flex:0 0 110px"><label>UDP port</label><input id="sip-udp_port" style="width:100%"></div>
      </div>
      <label>Outbound caller ID name</label><input id="sip-callerid_name" style="width:100%">
      <label>Transfer method</label><select id="sip-transfer_method" style="width:100%"><option value="bridge">bridge (Asterisk stays in the media path)</option><option value="refer">refer (SIP REFER to the softswitch)</option></select>
      <div class="row"><button id="tel-sip-save" class="primary" onclick="saveTelSip()">Save &amp; apply</button><span id="tel-smsg" class="hint" style="margin:0"></span></div>
      <div class="hint" style="margin-top:6px">"Save &amp; apply" re-registers the trunk without restarting Asterisk.</div>
</div></section>
<section class="tel-page" data-tel-page="agent" hidden><div data-tel-runtime-section hidden>
<p class="hint">Recognised callers reach their personal Prism agent. Other callers use the receptionist. Outbound missions run through PrismVox with the instructions below. Changes apply to new calls.</p>
<h3>Receptionist and outbound model</h3><select id="vox-model" style="width:100%"><option value="default">Default Prism model</option></select><div class="row"><button id="vox-model-save" class="primary" type="button" onclick="saveVoxModel()">Save model</button><span id="vox-model-status" class="hint" role="status"></span></div>
      <h2 style="margin-top:26px;font-size:15px">Conversation model — recognised callers</h2>
      <div class="row" style="gap:8px;align-items:center">
        <select id="tel-model" aria-label="Phone conversation model" style="flex:1" disabled><option>Loading…</option></select>
        <button id="tel-model-save" class="primary" onclick="saveVoiceModel()" disabled>Save model</button>
      </div>
      <div class="hint">Choose a model configured in AI provider. The default follows the deployment's AI model. Applies to new calls; the caller keeps their Prism profile, memory and permitted tools.</div>
      <div id="tel-model-msg" class="hint" role="status"></div>

      <h2 style="margin-top:26px;font-size:15px">Switchboard — unknown callers</h2>
      <div class="hint">Instructions for callers whose number does not match a Prism profile. Personal Prism memory and files are not shared with the switchboard.</div>
      <label>Personality</label><textarea id="tel-persona" placeholder="Loading…" style="min-height:150px"></textarea>
      <div class="row"><button class="primary" onclick="saveTelVoice()">Save switchboard</button><span id="tel-vmsg" class="hint" style="margin:0"></span></div>

<h3>Outbound instructions</h3><div class="hint">PrismVox runs outbound missions and conversations with transfer recipients, including when connected to Prism. The mission is added to these instructions for each outbound call.</div><div id="tel-outbound-model"></div>
<div class="row"><button class="primary" onclick="saveCfgFields(TEL_OUTBOUND_MODEL,'tel-omsg')">Save outbound instructions</button><span id="tel-omsg" class="hint" role="status"></span></div>      <h2 style="margin-top:18px;font-size:15px">Voice &amp; inbound greetings</h2>
      <div class="hint">The voice applies to all calls. Inbound greetings are spoken on pickup, before the first generated response.</div>
      <div class="row" style="gap:12px;align-items:flex-end">
        <div style="flex:1"><label>Voice</label><select id="tel-voice" style="width:100%"><option>Loading…</option></select></div>
        <button class="mini" onclick="previewVoice()">Preview</button>
      </div>
      <div id="tel-voicehint" class="hint" style="margin-top:4px"></div>
      <label style="margin-top:10px">Greeting — unknown caller <span class="hint" style="margin:0">— spoken on pickup, before the agent thinks at all</span></label>
      <textarea id="tel-greeting" placeholder="Loading…" style="min-height:60px"></textarea>
      <label style="margin-top:10px">Greeting — someone we recognise <span class="hint" style="margin:0">— when the number matches a profile. <code>%s</code> = their name; leave it out and the line is spoken as written.</span></label>
      <textarea id="tel-greeting-known" placeholder="Loading…" style="min-height:60px"></textarea>
      <div class="hint" style="margin-top:4px">The recognised-caller greeting is used when the incoming number matches an approved Prism profile.</div>
      <div class="row" style="gap:8px;margin-top:6px;align-items:center" id="tel-clonebox">
        <span class="filebtn"><input type="file" id="tel-clonefile" accept="audio/*" onchange="showPicked('tel-clonefile','tel-clonefilename')"><button type="button" onclick="document.getElementById('tel-clonefile').click()">Choose a voice sample…</button></span>
        <span id="tel-clonefilename" class="filename"></span>
        <input id="tel-clonename" placeholder="New voice name" style="width:170px">
        <button class="primary" onclick="cloneVoice()">Clone voice</button>
      </div>
      <div class="row"><button class="primary" onclick="saveTelVoiceGreeting()">Save voice &amp; greeting</button><span id="tel-gmsg" class="hint" style="margin:0"></span></div>

      <details style="margin-top:16px"><summary>Speech providers</summary>
        <iframe id="tel-speech-providers" title="Speech providers" data-src="/api/vox/voice/editor" style="width:100%;height:220px;border:0;margin-top:8px" loading="lazy"></iframe>
      </details>
</div></section>
<section class="tel-page" data-tel-page="access" hidden><div data-tel-runtime-section hidden>
<details id="tel-access" class="tel-section" hidden><summary>App access</summary><div class="tel-section-body">
<p class="hint">Members of the selected groups can consult the reception archive and use the Telephony app. Personal calls remain private.</p>
<div id="tel-access-choices"></div><div class="row"><button onclick="saveTelephonyAccess()">Save access</button><span id="tel-access-status" class="hint" role="status"></span></div></div></details>
<h3>Receptionist external tools (MCP)</h3><p class="hint">These servers provide tools to the public receptionist and outbound missions. The personal Prism agent keeps its own tools and permissions. Changes apply to new calls.</p><div id="tel-mcp-list"></div><details class="tel-section"><summary>Add an external tool server</summary><div id="tel-mcp-new" class="tel-section-body"></div></details><p id="tel-mcp-status" class="hint" role="status"></p>
<h3>Knowledge available to unknown callers</h3>      <label>Knowledge base <span class="hint" style="margin:0">— the one collection the switchboard may read, and read out loud to strangers</span></label>
      <select id="tel-kbsel" style="width:100%" onchange="saveVoiceKBChoice()"><option>Loading…</option></select>
      <div class="row" style="gap:8px;margin-top:4px;align-items:center">
        <span id="tel-kbmsg" class="hint" style="margin:0"></span>
        <button id="tel-kbdel" class="mini" style="display:none" onclick="deleteSelectedVoiceKB()">Delete this collection</button>
      </div>
      <div class="hint" style="margin-top:8px">Selection is saved immediately. <b>Documents in the selected collection are available to unknown callers.</b> Manage group documents in Knowledge Base.</div>
      <div class="hint" style="margin-top:8px">Upload documents to a switchboard collection:</div>
      <div class="row" style="gap:8px;margin-top:4px;align-items:center">
        <span class="filebtn"><input type="file" id="tel-kbfile" accept=".pdf,.txt,.md,.docx,.html,.csv" onchange="showPicked('tel-kbfile','tel-kbfilename')"><button type="button" onclick="document.getElementById('tel-kbfile').click()">Choose a file…</button></span>
        <span id="tel-kbfilename" class="filename"></span>
        <input id="tel-kbcol" placeholder="Collection name" style="width:150px">
        <button class="primary" onclick="uploadVoiceKB()">Upload</button>
        <span id="tel-kbupmsg" class="hint" style="margin:0"></span>
      </div>
      <h2 style="margin-top:26px;font-size:15px">Transfer directory</h2>
      <div class="hint">Everyone who can receive a transferred call: approved accounts with a phone number on their profile. <b>Blind</b> puts the caller straight through. <b>Attended</b> asks their name and reason first, announces them, and lets the recipient decline. Each person sets this on their own profile; you can override it here.</div>
      <div id="tel-dir">Loading…</div>

      <h2 style="margin-top:26px;font-size:15px">Outbound directory</h2>
      <div class="hint">People and businesses the agent can <b>call</b> — the plumber, a supplier, a customer. Ask it in plain words ("call the plumber and arrange an appointment") and it looks the number up here. These contacts receive outbound calls; they are not transfer destinations.</div>
      <div id="tel-out">Loading…</div>
      <div class="row" style="gap:6px;margin-top:8px">
        <input id="tel-outname" placeholder="Name, e.g. the plumber" style="flex:1">
        <input id="tel-outphone" placeholder="+596696…" style="width:170px">
        <button onclick="addOutContact()">Add</button>
        <span id="tel-outmsg" class="hint" style="margin:0"></span>
      </div>

</div></section>
<section class="tel-page" data-tel-page="advanced" hidden><div data-tel-runtime-section hidden>
<h3>Receptionist tools</h3><div id="tel-features"></div><div class="row"><button class="primary" onclick="saveCfgFields(TEL_FEATURES,'tel-fmsg')">Save features</button><span id="tel-fmsg" class="hint" role="status"></span></div>
<details class="tel-section"><summary>Legacy model endpoint</summary><div class="tel-section-body"><p class="hint">These fields apply when the model above is set to Keep the Vox deployment model.</p><h3>PrismVox model — unknown callers &amp; outbound calls</h3><div id="tel-inbound-model"></div>
<div class="row"><button id="tel-legacy-model-save" class="primary" onclick="saveCfgFields(TEL_INBOUND_MODEL,'tel-imsg')">Save PrismVox model</button><span id="tel-imsg" class="hint" role="status"></span></div></div></details>
      <h2 style="margin-top:26px;font-size:15px">Spoken phrases</h2>
      <div class="hint">Messages used at specific stages of a call or as fallbacks when no response is generated. Keep <code>%s</code> placeholders for names and the reason for the call.</div>
      <div id="tel-phrases"></div>
      <div class="row"><button class="primary" onclick="savePhrases()">Save phrases</button><span id="tel-pmsg" class="hint" style="margin:0"></span></div>

      <h2 style="margin-top:26px;font-size:15px">Pronunciation</h2>
      <div class="hint">How the agent reads acronyms aloud. Comma-separated.</div>
      <div id="tel-dict"></div>
      <div class="row"><button class="primary" onclick="saveDict()">Save dictionary</button><span id="tel-dmsg" class="hint" style="margin:0"></span></div>

      <h2 style="margin-top:26px;font-size:15px">Call handling</h2>
      <div class="hint">Limit the number of exchanges with a transfer recipient while the caller waits on hold.</div>
      <div id="tel-handling"></div>
      <div class="row"><button class="primary" onclick="saveHandling()">Save call handling</button><span id="tel-hmsg" class="hint" style="margin:0"></span></div>

</div></section>
<p id="tel-runtime-unavailable" class="hint" hidden>Connect PrismVox and resolve the connection checks before editing its configuration.</p>
    </div>
`

// adminTelephonyJS is the pane's script, inserted after the Logs block and before
// the Platform block.
const adminTelephonyJS = `// ── Telephony: switchboard persona (Prism /api/voice) + SIP trunk (proxied Vox /api/vox/sip) ──
const SIP_FIELDS=['registrar','registrar_ip','registrar_match','username','domain','transport_mode','tls_port','udp_port','callerid_name','transfer_method'];
let telVoiceDirty=false;
window.PrismTelephonyDirty=()=>telVoiceDirty;
window.PrismTelephonyDiscard=()=>{telVoiceDirty=false;const frame=$('tel-speech-providers');if(frame?.getAttribute('src'))frame.src=frame.src;notifyTelDirty();};
window.addEventListener('beforeunload',event=>{if(telVoiceDirty){event.preventDefault();event.returnValue='';}});
function telControls(ids){return ids.map(id=>$(id)).filter(Boolean);}
function notifyTelDirty(){
 if(typeof parent!=='undefined'&&parent!==window&&typeof location!=='undefined')parent.postMessage({type:'prism-telephony-dirty',dirty:!!window.PrismSettingsState?.dirty()||telVoiceDirty},location.origin);
}
function telNormalized(ids){const state=window.PrismSettingsState;if(state)state.saved(state.begin(telControls(ids)));notifyTelDirty();}
async function telRequest(url,options,controls=[]){
 try{return await(window.PrismSettingsState?.request?window.PrismSettingsState.request(url,options,controls):fetch(url,options));}
 finally{notifyTelDirty();}
}
if(typeof document!=='undefined')for(const type of ['input','change'])document.addEventListener(type,()=>queueMicrotask(notifyTelDirty));
const VOX_CONNECTION_FIELDS=['url','user','password','prism_url','prism_public_url'];
const VOX_CONNECTION_IDS={url:'vox-url',user:'vox-user',password:'vox-password',prism_url:'vox-prism-url',prism_public_url:'vox-public-url'};
let VOX_CONNECTION=null,VOX_SAVED_MODEL=null,VOX_TESTED_DRAFT=null,voxConnectionBusy=false,telRuntimeAvailable=false;
function showTelPage(name){
 if(typeof document==='undefined')return;
 document.querySelectorAll('[data-tel-page]').forEach(page=>page.hidden=page.dataset.telPage!==name);
 document.querySelectorAll('[data-tel-tab]').forEach(button=>button.setAttribute('aria-selected',String(button.dataset.telTab===name)));
}
function setTelRuntimeAvailable(available){
 telRuntimeAvailable=available;
 if(typeof document==='undefined')return;
 document.querySelectorAll('[data-tel-runtime-section]').forEach(section=>section.hidden=!available);
 document.querySelectorAll('[data-tel-tab]').forEach(button=>{if(button.dataset.telTab!=='connection')button.disabled=!available;});
 $('tel-runtime-unavailable').hidden=available;
 const speech=$('tel-speech-providers');if(available&&speech&&!speech.getAttribute('src'))speech.src=speech.dataset.src;
 if(!available)showTelPage('connection');
}
function voxConnectionPayload(){const values={};for(const key of VOX_CONNECTION_FIELDS)values[key]=key==='password'?$(VOX_CONNECTION_IDS[key]).value:$(VOX_CONNECTION_IDS[key]).value.trim();return values;}
function voxPairingUnsupported(){
 const url=($('vox-url')?.value||'').trim().replace(/\/+$/,''),user=($('vox-user')?.value||'').trim();
 return (VOX_CONNECTION?.pairing_supported===false&&url===VOX_CONNECTION.url&&user===VOX_CONNECTION.user)||(VOX_TESTED_DRAFT?.pairing_supported===false&&url===VOX_TESTED_DRAFT.url&&user===VOX_TESTED_DRAFT.user);
}
function connectionDraftChanged(){if(!voxConnectionBusy)$('vox-save').disabled=voxPairingUnsupported();}
function lockVoxConnection(busy){
 voxConnectionBusy=busy;
 for(const id of [...Object.values(VOX_CONNECTION_IDS),'vox-save','vox-test','vox-disconnect'])if($(id))$(id).disabled=busy||(id==='vox-save'&&voxPairingUnsupported());
}
function renderVoxHealth(data){
 const host=$('vox-health');host.replaceChildren();
 const states=[['configured','PrismVox connection'],['reachable','Prism → PrismVox'],['authenticated','API authentication'],['reverse_connected','PrismVox → Prism']];
 for(const [key,label]of states){const node=document.createElement('span');node.textContent=(data[key]===true?'✓ ':data[key]===false?'× ':'— ')+label;host.append(node);}
 $('vox-connection-detail').textContent=[...new Set([data.detail,data.reverse_detail].filter(value=>typeof value==='string'&&value.trim()))].join(' ');
}
async function loadVoxModels(selected){
 const catalog=await jget('/api/models?qualified=1');const select=$('vox-model');if(!catalog)return;
 select.replaceChildren(new Option('Default Prism model','default'),new Option('Keep the Vox deployment model',''));
 for(const model of catalog.models||[])select.add(new Option(catalog.labels?.[model]||model,model));
 const chosen=catalog.aliases?.[selected]||selected;
 if(chosen && ![...select.options].some(option=>option.value===chosen))select.add(new Option(chosen+' (unavailable)',chosen));
 select.value=chosen??'default';updateLegacyModelState();
}
async function loadVoxConnection(){
 const modelDraft=$('vox-model').value,preserveModelDraft=VOX_CONNECTION!==null&&VOX_SAVED_MODEL!==null&&modelDraft!==VOX_SAVED_MODEL;
 lockVoxConnection(true);
 try{
  const response=await fetch('/api/vox-connection');
  if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not read the connection'));
  const data=await response.json();VOX_CONNECTION=data;VOX_TESTED_DRAFT=null;
  for(const key of VOX_CONNECTION_FIELDS)if(key!=='password')$(VOX_CONNECTION_IDS[key]).value=data[key]||((key==='prism_url'||key==='prism_public_url')?location.origin:'');
  $('vox-password').value='';$('vox-password').placeholder=data.password_configured?'Saved password · leave empty to keep':'Password';
  $('vox-disconnect').hidden=!data.configured;renderVoxHealth(data);
  const available=data.configured===true&&data.reachable===true&&data.authenticated===true;
  setTelRuntimeAvailable(available);
  $('tel-connection-summary').textContent=available?(data.reverse_connected===true?'PrismVox is connected. Configure the receptionist here and use Telephony to follow calls and missions.':'Prism can configure PrismVox. Check the return connection so recognised callers and the shared model can reach Prism.'):data.configured?'The connection is saved, but PrismVox is unavailable or rejected authentication. Update the connection below.':'Connect PrismVox to add telephone capabilities to your Prism agent.';
  await loadVoxModels(data.url?data.model:'default');VOX_SAVED_MODEL=$('vox-model').value;
  $('vox-model').disabled=data.pairing_supported===false;$('vox-model-save').disabled=data.pairing_supported===false;
  if(preserveModelDraft&&[...$('vox-model').options].some(option=>option.value===modelDraft))$('vox-model').value=modelDraft;
  return available;
 }catch(error){setTelRuntimeAvailable(false);$('vox-connection-status').textContent=error.message;return false;}
 finally{lockVoxConnection(false);}
}
async function saveVoxConnection(){
 if(voxConnectionBusy)return;
 if(voxPairingUnsupported()){$('vox-connection-status').textContent='Update PrismVox to save the two-way connection. Its existing settings remain available.';return;}
 const payload=voxConnectionPayload(),wasAvailable=telRuntimeAvailable;
 if(!VOX_CONNECTION?.url)payload.model='default';
 lockVoxConnection(true);$('vox-connection-status').textContent='Connecting…';
 try{
  const response=await telRequest('/api/vox-connection',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)},telControls(Object.values(VOX_CONNECTION_IDS)));
  if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not save the connection'));
  const available=await loadVoxConnection();telNormalized(Object.values(VOX_CONNECTION_IDS));
  $('vox-connection-status').textContent=available&&VOX_CONNECTION?.reverse_connected===true?'Connection saved.': 'Connection saved; resolve the checks above.';
  if(available&&!wasAvailable)await loadTelephonyData();
 }catch(error){$('vox-connection-status').textContent=error.message;}
 finally{lockVoxConnection(false);}
}
async function testVoxConnection(){
 if(voxConnectionBusy)return;const payload=voxConnectionPayload();lockVoxConnection(true);$('vox-connection-status').textContent='Testing…';
 try{
  const response=await fetch('/api/vox-connection/test',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
  if(!response.ok)throw new Error(await telephonyResponseError(response,'Connection test failed'));
  const data=await response.json();VOX_TESTED_DRAFT={url:payload.url.replace(/\/+$/,''),user:payload.user,pairing_supported:data.pairing_supported};renderVoxHealth(data);$('vox-connection-status').textContent=data.reachable&&data.authenticated?'API connection verified. Your draft has not been saved.':'Connection test failed. Your draft has not been saved.';
 }catch(error){$('vox-connection-status').textContent=error.message;}
 finally{lockVoxConnection(false);}
}
async function disconnectVox(){
 if(voxConnectionBusy||!await PrismModal.confirm('Disconnect PrismVox from Prism? Existing call archives are retained.'))return;
 lockVoxConnection(true);
 try{
  const response=await telRequest('/api/vox-connection',{method:'DELETE'},telControls(Object.values(VOX_CONNECTION_IDS)));
  if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not disconnect'));
  const result=await response.json().catch(()=>({}));
  await loadVoxConnection();telNormalized(Object.values(VOX_CONNECTION_IDS));$('vox-connection-status').textContent=typeof result.detail==='string'?result.detail:'Disconnected.';
 }catch(error){$('vox-connection-status').textContent=error.message;}
 finally{lockVoxConnection(false);}
}
async function saveVoxModel(){
 if(VOX_CONNECTION?.pairing_supported===false){$('vox-model-status').textContent='Update PrismVox to select a Prism model. The Vox deployment model remains configurable under Advanced.';return;}
 const select=$('vox-model'),status=$('vox-model-status');select.disabled=true;status.textContent='Saving…';
 try{
  const response=await telRequest('/api/vox-connection',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({model:select.value})},[select]);
  if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not save the model'));
  if(VOX_CONNECTION)VOX_CONNECTION.model=select.value;VOX_SAVED_MODEL=select.value;updateLegacyModelState();status.textContent='Saved · applies to new calls';
 }catch(error){status.textContent=error.message;}finally{select.disabled=false;}
}
function updateTelSipTransport(){if(typeof document==='undefined')return;const mode=$('sip-transport_mode')?.value||'tls';document.querySelectorAll('[data-tel-transport]').forEach(field=>field.hidden=field.dataset.telTransport!==mode);}
async function refreshTelSipStatus(){const status=await jget('/api/vox/sip/status');$('tel-sipstatus').textContent=status?(status.endpoint_state==='online'?'Line online': 'Line state: '+(status.endpoint_state||'unknown')):'Line status unavailable';}
// The switchboard reads a dedicated, reserved RAG scope ("voice"); documents are
// managed right here, so it's independent from any group.
// One collection, chosen — not "whatever sits in a scope". The two origins are in
// one list because it is one decision, but each option says which it is: what the
// switchboard reads, an unknown caller can hear.
async function loadVoiceKB(){
 const sel=$('tel-kbsel');if(!sel)return;
 const d=await jget('/api/voice/kb');if(!d){sel.disabled=true;$('tel-kbmsg').textContent='Could not load the public knowledge selection.';return;}sel.disabled=false;const choices=d.choices||[];
 const own=choices.filter(c=>c.origin==='switchboard'), grp=choices.filter(c=>c.origin==='group');
 const opt=c=>'<option value="'+esc(c.value)+'"'+(c.value===(d&&d.selected)?' selected':'')+'>'+esc(c.label)+' ('+(c.docs||0)+' docs)</option>';
 let html='<option value=""'+(!(d&&d.selected)?' selected':'')+'>— none: the switchboard answers from its personality alone —</option>';
 if(own.length)html+='<optgroup label="Switchboard only — isolated, nobody else can read it">'+own.map(opt).join('')+'</optgroup>';
 if(grp.length)html+='<optgroup label="Group collections — readable by unknown callers once chosen">'+grp.map(opt).join('')+'</optgroup>';
 sel.innerHTML=html;
 const m=$('tel-kbmsg');
 if(m)m.textContent=(d&&d.selected)?'':'No base selected — the switchboard will say it does not have the information.';
 // Deletable only when the switchboard owns it. A group's collection is the
 // group's, managed in RAG — unselecting it here must never destroy it.
 const sc=choices.find(c=>c.value===(d&&d.selected));
 const del=$('tel-kbdel');
 if(del)del.style.display=(sc&&sc.origin==='switchboard')?'':'none';
}
async function deleteSelectedVoiceKB(){
 const sel=$('tel-kbsel');const label=sel.options[sel.selectedIndex].text.replace(/ \(\d+ docs\)$/,'');
 if(!await deleteVoiceKB(label))return;
 await fetch('/api/voice/kb',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({collection:''})});
 loadVoiceKB();
}
async function saveVoiceKBChoice(){
 const m=$('tel-kbmsg');m.textContent='Saving…';
 const r=await telRequest('/api/voice/kb',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({collection:$('tel-kbsel').value})},telControls(['tel-kbsel']));
 m.textContent=r.ok?'✓ Saved':'Failed ('+r.status+')';
 if(r.ok)setTimeout(()=>m.textContent='',1800);
}
async function uploadVoiceKB(){
 const f=$('tel-kbfile').files[0];const m=$('tel-kbupmsg');
 const name=($('tel-kbcol').value||'switchboard').trim();
 if(!f){m.textContent='Pick a file first.';return;}
 m.textContent='Uploading & indexing…';
 // Ensure the collection carries a description so the agent knows when to search it.
 await fetch('/api/rag/collections?scope=voice',{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({name,description:'Public information for phone callers: opening hours, prices, services offered, and frequently asked questions.'})});
 const fd=new FormData();fd.append('collection',name);fd.append('file',f);
 let r;try{r=await telRequest('/api/rag/upload?scope=voice',{method:'POST',body:fd},telControls(['tel-kbcol']));}catch(e){m.textContent='Failed: '+e.message;return;}
 m.textContent=r.ok?'✓ Added — select it above to put it on the line':'Failed ('+r.status+')';
 $('tel-kbfile').value='';$('tel-kbfilename').textContent='';
 loadVoiceKB();setTimeout(()=>m.textContent='',4000);
}
async function deleteVoiceKB(name){
 if(!await PrismModal.confirm('Delete "'+name+'" and all its documents?',{danger:true}))return false;
 const response=await fetch('/api/rag/collections?scope=voice&name='+encodeURIComponent(name),{method:'DELETE'});
 if(!response.ok){$('tel-kbmsg').textContent=await telephonyResponseError(response,'Could not delete the collection');return false;}
 loadVoiceKB();return true;
}
window.addEventListener('message',event=>{
 const frame=$('tel-speech-providers');
 if(event.origin!==location.origin||event.source!==frame?.contentWindow)return;
 if(event.data?.type==='prismvox-voice-dirty'||event.data?.type==='prismconnect-voice-dirty'){telVoiceDirty=event.data.dirty===true;notifyTelDirty();}
 if(event.data?.type==='prismconnect-voice-height')frame.style.height=Math.min(900,Math.max(160,event.data.height))+'px';
 if(event.data?.type==='prismconnect-voice-saved')loadTelVoiceGreeting();
});
async function loadTelephony(){
 loadTelephonyAccess();
 if(await loadVoxConnection())await loadTelephonyData();
}
function updateLegacyModelState(){
 if(typeof document==='undefined')return;const disabled=VOX_CONNECTION?.pairing_supported!==false&&!!VOX_CONNECTION?.model;
 for(const id of ['cf-llm_model_standard','cf-llm_url_standard','tel-legacy-model-save'])if($(id))$(id).disabled=disabled;
}
async function loadTelephonyData(){
 const voice=await jget('/api/voice');
 if(voice){$('tel-persona').value=voice.personality||'';loadVoiceModel(voice.model||'');}
 else{setTelRuntimeAvailable(false);$('vox-connection-status').textContent='Could not load the Prism phone agent settings. Retry the connection.';return;}
 await Promise.all([loadVoiceKB(),loadTelDirectory(),loadOutContacts(),loadTelVoiceGreeting(),loadPhoneCfg(),loadTelMCP()]);
 const sip=await jget('/api/vox/sip');
 if(sip){SIP_FIELDS.forEach(field=>{if($('sip-'+field))$('sip-'+field).value=sip['sip_'+field]||(field==='transport_mode'?'tls':field==='tls_port'?'5061':field==='udp_port'?'5060':'');});updateTelSipTransport();}
 else{setTelRuntimeAvailable(false);$('vox-connection-status').textContent='Could not load SIP settings. Retry the connection.';}
 await refreshTelSipStatus();
}
function telMCPField(label,element){const wrapper=document.createElement('label');wrapper.append(document.createTextNode(label),element);return wrapper;}
function telMCPForm(server={},onCreated){
 const form=document.createElement('form'),fields=document.createElement('div');fields.className='tel-connection-fields';
 const name=document.createElement('input');name.value=server.name||'';name.required=true;name.maxLength=200;
 const type=document.createElement('select');for(const [value,label]of [['http','Streamable HTTP'],['sse','Legacy SSE']])type.add(new Option(label,value));
 if(server.type==='stdio'){const option=new Option('Stdio — select HTTP or SSE to edit','stdio');option.disabled=true;type.add(option);}type.value=server.type||'http';
 const url=document.createElement('input');url.type='url';url.required=true;url.value=server.url||'';url.placeholder='https://tools.example.com/mcp';
 const token=document.createElement('input');token.type='password';token.autocomplete='new-password';token.placeholder=server.has_auth_token?'Saved token · leave empty to keep':'Optional bearer token';
 const clear=document.createElement('input');clear.type='checkbox';
 const enabled=document.createElement('input');enabled.type='checkbox';enabled.checked=server.enabled!==false;enabled.defaultChecked=enabled.checked;
 fields.append(telMCPField('Name',name),telMCPField('Transport',type),telMCPField('URL',url),telMCPField('Bearer token',token),telMCPField('Remove saved token',clear),telMCPField('Enabled',enabled));
 const controls=[name,type,url,token,clear,enabled],button=document.createElement('button'),status=document.createElement('span');button.type='submit';button.className='primary';button.textContent=server.id?'Save server':'Add server';status.className='hint';status.setAttribute('role','status');
 form.append(fields,button,status);
 form.onsubmit=async event=>{
  event.preventDefault();if(button.disabled)return;
  if(!['http','sse'].includes(type.value)){status.textContent='Select HTTP or SSE.';return;}
  const values={name:name.value.trim(),type:type.value,url:url.value.trim(),auth_token:token.value,enabled:enabled.checked};
  if(server.id)values.clear_auth_token=clear.checked;
  if(token.value&&clear.checked){status.textContent='Enter a replacement token or remove the saved token, not both.';return;}
  button.disabled=true;status.textContent='Saving…';
  try{
   const response=await telRequest('/api/vox/mcp'+(server.id?'/'+server.id:''),{method:server.id?'PUT':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(values)},controls);
   if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not save the MCP server'));
   const saved=await response.json();
   const info={id:server.id||saved.id,name:values.name,type:values.type,url:values.url,enabled:values.enabled,has_auth_token:!!values.auth_token||(!clear.checked&&!!server.has_auth_token)};
   token.value='';clear.checked=false;token.placeholder=info.has_auth_token?'Saved token · leave empty to keep':'Optional bearer token';
   const state=window.PrismSettingsState;if(state)state.saved(state.begin(controls));notifyTelDirty();
   if(!server.id){onCreated(info);form.reset();if(state)state.saved(state.begin(controls));enabled.checked=true;}
   else{Object.assign(server,info);form.closest('details').querySelector('summary').textContent=info.name+' · '+info.type;}
   status.textContent='Saved · applies to new calls';
  }catch(error){status.textContent=error.message;}finally{button.disabled=false;}
 };
 return form;
}
function telMCPCard(server){
 const card=document.createElement('details'),summary=document.createElement('summary'),body=document.createElement('div');card.className='tel-section';body.className='tel-section-body';summary.textContent=server.name+' · '+server.type;card.append(summary,body);
 body.append(telMCPForm(server));
 const actions=document.createElement('div'),discover=document.createElement('button'),remove=document.createElement('button'),tools=document.createElement('div');actions.className='row';discover.type=remove.type='button';discover.textContent='Discover tools';remove.textContent='Remove server';actions.append(discover,remove);body.append(actions,tools);
 discover.onclick=async()=>{
  discover.disabled=true;tools.textContent='Discovering tools…';
  try{
   const response=await fetch('/api/vox/mcp/'+server.id+'/probe',{method:'POST'});
   if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not connect to the MCP server'));
   await loadTools();
  }catch(error){tools.textContent=error.message;}finally{discover.disabled=false;}
 };
 async function loadTools(){
  const data=await jget('/api/vox/mcp/'+server.id+'/tools');if(!Array.isArray(data)){tools.textContent='Could not load tools.';return;}
  tools.replaceChildren();if(!data.length)tools.textContent='No tools discovered yet.';
  for(const tool of data){
   const label=document.createElement('label'),checkbox=document.createElement('input'),description=document.createElement('span');checkbox.type='checkbox';checkbox.checked=tool.enabled!==false;description.textContent=tool.name+(tool.description?' — '+tool.description:'');label.append(checkbox,description);tools.append(label);
   checkbox.onchange=async()=>{
    checkbox.disabled=true;
    try{const response=await telRequest('/api/vox/mcp/'+server.id+'/tools/'+encodeURIComponent(tool.name)+'?enabled='+checkbox.checked,{method:'PATCH'},[checkbox]);if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not save tool access'));}
    catch(error){$('tel-mcp-status').textContent=error.message;}
    finally{checkbox.disabled=false;}
   };
  }
 }
 let loaded=false;card.addEventListener('toggle',()=>{if(card.open&&!loaded){loaded=true;loadTools();}});
 remove.onclick=async()=>{
  if(!await PrismModal.confirm('Remove '+server.name+' from the receptionist tools?',{danger:true}))return;
  remove.disabled=true;
  try{const response=await fetch('/api/vox/mcp/'+server.id,{method:'DELETE'});if(!response.ok)throw new Error(await telephonyResponseError(response,'Could not remove the server'));card.remove();notifyTelDirty();}
  catch(error){$('tel-mcp-status').textContent=error.message;remove.disabled=false;}
 };
 return card;
}
async function loadTelMCP(){
 const data=await jget('/api/vox/mcp'),list=$('tel-mcp-list'),create=$('tel-mcp-new');
 if(!Array.isArray(data)){list.textContent='Could not load the receptionist tool servers.';return;}
 list.replaceChildren(...data.map(server=>telMCPCard(server)));
 create.replaceChildren(telMCPForm({},server=>{list.append(telMCPCard(server));}));
}
async function loadTelephonyAccess(){
 const me=await jget('/api/me');if(!me?.multiUser)return;
 const data=await jget('/api/vox-workspace/access');if(!data)return;
 $('tel-access').hidden=false;const choices=$('tel-access-choices');choices.replaceChildren();
 for(const group of data.availableGroups||[]){const label=document.createElement('label'),box=document.createElement('input');box.type='checkbox';box.value=group.id;box.checked=(data.groups||[]).includes(group.id);label.append(box,document.createTextNode(' '+group.name));choices.append(label);}
}
async function saveTelephonyAccess(){
 const status=$('tel-access-status');status.textContent='Saving…';
 try{const groups=[...$('tel-access-choices').querySelectorAll('input:checked')].map(box=>Number(box.value));const r=await telRequest('/api/vox-workspace/access',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({groups})},[...$('tel-access-choices').querySelectorAll('input')]);status.textContent=r.ok?'Saved':'Failed ('+r.status+')';}catch(e){status.textContent='Could not save access.';}
}
async function loadVoiceModel(selected){
 const sel=$('tel-model'), button=$('tel-model-save');
 try{
  const data=await jget('/api/models');if(!data)throw new Error('Could not load models.');
  const choices=data.models||[];sel.replaceChildren(new Option('Default AI model',''));
  for(const model of choices)sel.add(new Option(data.labels?.[model]||model,model));
  if(selected&&!choices.includes(selected))sel.add(new Option(selected+' (unavailable)',selected));
  sel.value=selected;sel.disabled=false;button.disabled=false;
 }catch(e){$('tel-model-msg').textContent=e.message;}
}
async function saveVoiceModel(){
 const button=$('tel-model-save'), msg=$('tel-model-msg');button.disabled=true;msg.textContent='Saving…';
 try{
  const r=await telRequest('/api/voice',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({model:$('tel-model').value}),signal:AbortSignal.timeout(15000)},telControls(['tel-model']));
  if(!r.ok){const d=await r.json();throw new Error(d.error||'Could not save the phone model.');}
  msg.textContent='Saved · applies to new calls';
 }catch(e){msg.textContent=e.message;}finally{button.disabled=false;}
}
async function saveTelVoice(){
 const message=$('tel-vmsg');message.textContent='Saving…';
 try{
  const response=await telRequest('/api/voice',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({personality:$('tel-persona').value})},telControls(['tel-persona']));
  message.textContent=response.ok?'✓ Saved':await telephonyResponseError(response,'Could not save the receptionist');
  if(response.ok)setTimeout(()=>message.textContent='',2500);
 }catch(error){message.textContent=error.message;}
}
// ── Voice & greeting (every call) ──
// The voice lives in the phone stack (TTS engine + the ElevenLabs account), so it is
// read and written through the Vox proxy. Cloning only exists on ElevenLabs — the
// local engines have a fixed voice, so the controls are hidden rather than offered
// and then rejected.
async function loadTelVoiceGreeting(){
 const sel=$('tel-voice');
 const b=await jget('/api/vox/tts/backend');
 const cloneable=!!(b&&b.clone_enabled);
 $('tel-clonebox').style.display=cloneable?'flex':'none';
 $('tel-voicehint').textContent=cloneable
   ? 'Voices come from your ElevenLabs account. Add more from the ElevenLabs Voice Library and they appear here.'
   : b ? 'The '+(b.backend||'local')+' voice engine does not support voice cloning.'
       : 'Voice engine status unavailable. Check the phone stack; cloning is disabled until its capabilities are known.';
 const d=await jget('/api/vox/tts/voices');
 const voices=(d&&d.voices)||[];
 sel.innerHTML=voices.length
   ? voices.map(n=>'<option'+(n===(d.current||'')?' selected':'')+'>'+esc(n)+'</option>').join('')
   : '<option value="">(no voice available)</option>';
}
async function saveTelVoiceGreeting(){
 const m=$('tel-gmsg');m.textContent='Saving…';
 const name=$('tel-voice').value;
 if(name){
  const rv=await telRequest('/api/vox/tts/voice',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({name})},telControls(['tel-voice']));
  if(!rv.ok){m.textContent='Voice failed ('+rv.status+')';return;}
 }
 const rc=await telRequest('/api/vox/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({values:{
  greeting:$('tel-greeting').value,
  greeting_known:$('tel-greeting-known').value}})},telControls(['tel-greeting','tel-greeting-known']));
 m.textContent=rc.ok?'✓ Saved':'Greeting failed ('+rc.status+')';
 setTimeout(()=>m.textContent='',2500);
}
async function previewVoice(){
 const m=$('tel-gmsg');const name=$('tel-voice').value;
 if(!name){m.textContent='Pick a voice first.';return;}
 m.textContent='Synthesizing…';
 const text=$('tel-greeting').value.trim()||'Hello, how can I help you today?';
 let r;try{r=await fetch('/api/vox/tts/preview',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text,voice:name})});}
 catch(e){m.textContent='Failed: '+e.message;return;}
 if(!r.ok){m.textContent='Failed ('+r.status+')';return;}
 const url=URL.createObjectURL(await r.blob());
 new Audio(url).play().catch(()=>{});
 m.textContent='▶ Playing';setTimeout(()=>m.textContent='',2500);
}
async function cloneVoice(){
 const f=$('tel-clonefile').files[0];const m=$('tel-gmsg');
 if(!f){m.textContent='Pick an audio sample first.';return;}
 m.textContent='Cloning… (this uploads the sample to ElevenLabs)';
 const fd=new FormData();fd.append('file',f);fd.append('name',$('tel-clonename').value.trim());
 let r;try{r=await telRequest('/api/vox/tts/voices',{method:'POST',body:fd},telControls(['tel-clonename']));}catch(e){m.textContent='Failed: '+e.message;return;}
 if(!r.ok){m.textContent='Failed ('+r.status+') — the API key may lack voice-write permission.';return;}
 m.textContent='✓ Voice cloned';
 $('tel-clonefile').value='';$('tel-clonefilename').textContent='';$('tel-clonename').value='';telNormalized(['tel-clonename']);
 loadTelVoiceGreeting();setTimeout(()=>m.textContent='',3500);
}

// ── Phone-stack config (phrases, dictionary, call handling) ──
// All of it lives in Vox's config table and is read/written through the proxy — the
// cockpit drives the phone stack's own API, it does not reimplement it.
// [key, label, hint, kind] — kind: 'ta' textarea, 'in' input, 'num' number.
const TEL_PHRASES=[
 ['phrase_hold','On hold — attended transfer','Always spoken. Precedes putting the caller on hold.','ta'],
 ['phrase_transfer_now','Transferring now — blind transfer','Always spoken, right before the irreversible transfer.','ta'],
 ['phrase_connecting','Connecting — recipient accepted','Always spoken. <code>%s</code> = the name of the recipient.','ta'],
 ['phrase_still_there','Still there? — after silence','Fallback: the agent normally generates a re-prompt that names the pending topic.','ta'],
 ['phrase_unknown_contact','Unknown/unreachable contact','Fallback. <code>%s</code> = the name asked for.','ta'],
 ['phrase_farewell','Farewell — before hanging up','Fallback.','ta'],
 ['phrase_ask_name','Asking the name of the caller','Attended transfer.','ta'],
 ['phrase_ask_reason','Asking what the call is about','Attended transfer.','ta'],
 ['phrase_announce','Announcement to the recipient','Fallback if generation fails. Two <code>%s</code>: caller name, then reason.','ta'],
];
// TTS only. There is no transcription dictionary any more: the phone stack runs on
// ElevenLabs Scribe, which accepts no lexical biasing (the local Whisper that did is
// gone). A rare confident mis-hearing on a short, context-free word is the price.
const TEL_DICT=[
 ['whisper_hotwords','Transcription hints','Optional words and names, when supported by the speech provider.','in'],
 ['tts_spell_words','Always spell out','Read the letters individually, e.g. "IP".','in'],
 ['tts_spell_exceptions','Never spell out','Read as a word despite looking like an acronym, e.g. "OK".','in'],
];
const TEL_HANDLING=[
 ['transfer_dialog_turns','Max exchanges with a transfer recipient','1–10. Empty uses 3.','num'],
];
const TEL_FEATURES=[
 ['rag_enabled','Receptionist knowledge search','Allow the receptionist to search the selected public knowledge base.','bool'],
 ['mcp_enabled','Receptionist MCP tools','Allow the receptionist to use tools configured in PrismVox. Personal Prism agents keep their own tool permissions.','bool'],
];
const TEL_INBOUND_MODEL=[
 ['llm_model_standard','PrismVox conversation model','Used for unknown callers, outbound calls, transfer recipients, summaries and console chats. Empty uses the Vox deployment default.','in'],
 ['llm_url_standard','Model API root (optional)','Include /v1. Empty uses the Vox deployment gateway; separate endpoints receive no gateway API key.','url'],
];
const TEL_OUTBOUND_MODEL=[
 ['outbound_system_prompt','Instructions — outbound calls','The opening is generated from the mission.','ta'],
];
let TEL_CFG={};
function renderCfgFields(boxId,spec){
 $(boxId).innerHTML=spec.map(([k,label,hint,kind])=>{
  const v=esc(TEL_CFG[k]||'');
  const field=kind==='bool'?'<input type="checkbox" id="cf-'+k+'"'+(TEL_CFG[k]!=='false'?' checked':'')+'>':kind==='ta'?'<textarea id="cf-'+k+'" style="min-height:44px">'+v+'</textarea>'
    :'<input id="cf-'+k+'"'+(kind==='num'?' type="number" min="1" max="10"':kind==='url'?' type="url"':'')+' value="'+v+'" style="width:100%">';
  const markup = '<label style="margin-top:8px">'+label+'</label><div class="hint" style="margin:0 0 3px">'+hint+'</div>'+field;
  return kind==='url'?'<details style="margin-top:10px"><summary>Advanced: model endpoint</summary>'+markup+'</details>':markup;
 }).join('');
}
async function loadPhoneCfg(){
 const config=await jget('/api/vox/config');
 if(!config){setTelRuntimeAvailable(false);$('vox-connection-status').textContent='Could not load PrismVox settings. Your existing configuration has not been changed.';return;}
 TEL_CFG=config;
 renderCfgFields('tel-features',TEL_FEATURES);
 renderCfgFields('tel-phrases',TEL_PHRASES);
 renderCfgFields('tel-dict',TEL_DICT);
 renderCfgFields('tel-handling',TEL_HANDLING);
 renderCfgFields('tel-inbound-model',TEL_INBOUND_MODEL);
 renderCfgFields('tel-outbound-model',TEL_OUTBOUND_MODEL);
 updateLegacyModelState();
 $('tel-greeting').value=TEL_CFG.greeting||'';
 // Empty in the config table = the compiled-in default is in force. Showing it
 // rather than an empty box is what makes the field editable instead of a
 // mystery: an admin sees the sentence their callers actually hear.
 $('tel-greeting-known').value=TEL_CFG.greeting_known||DEFAULT_GREETING_KNOWN;
}
const DEFAULT_GREETING_KNOWN="Hello %s, this is your assistant. How can I help you?";
async function saveCfgFields(spec,msgId){
 const message=$(msgId);message.textContent='Saving…';
 const values={};spec.forEach(([key])=>{const field=$('cf-'+key);if(field)values[key]=field.type==='checkbox'?String(field.checked):field.value;});
 try{
  const response=await telRequest('/api/vox/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({values})},telControls(spec.map(([key])=>'cf-'+key)));
  message.textContent=response.ok?'✓ Saved':await telephonyResponseError(response,'Could not save these settings');
  if(response.ok){Object.assign(TEL_CFG,values);setTimeout(()=>message.textContent='',2500);}
 }catch(error){message.textContent=error.message;}
}
// The transfer directory is Prism's own user list, not a telephony table: being
// transferable means having an account here with a number on it. Shown where an
// admin thinks about transfers rather than buried in the Users tab, which does
// not display phone numbers at all.
async function loadTelDirectory(){
 const box=$('tel-dir');if(!box)return;
 const d=await jget('/api/voice/directory');if(!d){box.textContent='Could not load the transfer directory.';return;}const rows=d.entries||[];
 if(!rows.length){box.innerHTML='<div class="hint">Nobody has a phone number on their profile yet, so no call can be transferred. Add one in Settings → Profile.</div>';return;}
 box.innerHTML='<table><tr><th>Name</th><th>Number</th><th>Transfer</th></tr>'+rows.map(e=>{
  const att=e.transfer==='attended';
  return '<tr><td>'+esc(e.name)+'</td><td>'+esc(e.phone)+'</td><td>'+
   '<select onchange="setTelTransfer('+e.id+',this.value,this)">'+
   '<option value="blind"'+(att?'':' selected')+'>Blind — put straight through</option>'+
   '<option value="attended"'+(att?' selected':'')+'>Attended — announce first</option>'+
   '</select></td></tr>';}).join('')+'</table>';
}
async function setTelTransfer(id,kind,control){
 const r=await telRequest('/api/admin/users',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id,action:kind==='attended'?'transfer_attended':'transfer_blind'})},control?[control]:[]);
 if(!r.ok)alert('Could not save: HTTP '+r.status);
 loadTelDirectory();
}

// The outbound directory lives in Vox — it is telephony data, and Vox resolves
// the name when a call is placed. Edited here because Prism is the only console.
async function loadOutContacts(){
 const box=$('tel-out');if(!box)return;
 const d=await jget('/api/vox/contacts');if(!d){box.textContent='Could not load the outbound directory.';return;}const rows=Array.isArray(d)?d:(d.items||[]);
 if(!rows.length){box.innerHTML='<div class="hint">Nobody yet. Add a contact below and the agent can call them by name.</div>';return;}
 box.innerHTML='<table><tr><th>Name</th><th>Number</th><th></th></tr>'+rows.map(c=>
  '<tr><td>'+esc(c.name)+'</td><td>'+esc(c.phone_number||'')+'</td>'+
  '<td style="text-align:right"><button onclick="rmOutContact('+c.id+',\''+esc(c.name).replace(/'/g,"\\'")+'\')">Remove</button></td></tr>').join('')+'</table>';
}
async function addOutContact(){
 const m=$('tel-outmsg');const name=$('tel-outname').value.trim();const phone=$('tel-outphone').value.trim();
 if(!name||!phone){m.textContent='Name and number required';return;}
 m.textContent='Saving…';
 const r=await telRequest('/api/vox/contacts',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name,phone_number:phone})},telControls(['tel-outname','tel-outphone']));
 if(r.ok){$('tel-outname').value='';$('tel-outphone').value='';telNormalized(['tel-outname','tel-outphone']);m.textContent='✓ Added';setTimeout(()=>m.textContent='',1500);loadOutContacts();}
 else m.textContent='Failed ('+r.status+')';
}
async function rmOutContact(id,name){
 if(!confirm('Remove '+name+' from the outbound directory? The agent will no longer be able to call them by name.'))return;
 await fetch('/api/vox/contacts/'+id,{method:'DELETE'});loadOutContacts();
}

const savePhrases =()=>saveCfgFields(TEL_PHRASES,'tel-pmsg');
const saveDict    =()=>saveCfgFields(TEL_DICT,'tel-dmsg');
const saveHandling=()=>saveCfgFields(TEL_HANDLING,'tel-hmsg');

// Outbound calls are deliberately NOT an admin form here. When docked you place a call
// by asking the agent — that is what its place_call tool is for. A form would be a
// second, dumber door onto the same queue.

async function telephonyResponseError(r,fallback){
 const d=await r.json().catch(()=>null);
 return d&&typeof d.detail==='string'?d.detail:d&&typeof d.error==='string'?d.error:fallback+' (HTTP '+r.status+')';
}
async function saveTelSip(){
 const m=$('tel-smsg'),button=$('tel-sip-save');
 if(button&&button.disabled)return;
 if(button)button.disabled=true;
 m.textContent='Saving…';let saved=false;
 try{
  const current=await jget('/api/vox/sip');
  if(!current){m.textContent='Cannot load current SIP settings; nothing was saved.';return;}
  const body={};
  // Preserve hidden settings even against an older phone-stack API.
  [...SIP_FIELDS,'registrar_match','transport_mode','udp_port'].forEach(f=>{
   if(Object.prototype.hasOwnProperty.call(current,'sip_'+f))body['sip_'+f]=current['sip_'+f];
  });
  SIP_FIELDS.forEach(f=>{if($('sip-'+f))body['sip_'+f]=$('sip-'+f).value.trim();});
  body.sip_password=$('sip-password').value; // blank explicitly means unchanged
  const r=await telRequest('/api/vox/sip',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)},telControls(SIP_FIELDS.map(key=>'sip-'+key).concat('sip-password')));
  if(!r.ok){m.textContent=await telephonyResponseError(r,'Could not save SIP settings');return;}
  saved=true;$('sip-password').value='';telNormalized(['sip-password']);m.textContent='Saved. Applying…';
  const applied=await fetch('/api/vox/sip/apply',{method:'POST'});
  if(!applied.ok){m.textContent='Saved, but could not apply: '+await telephonyResponseError(applied,'Reload failed');return;}
  const result=await applied.json();
  if(result.ok!==true){m.textContent='Saved, but could not apply: '+(result.detail||'Reload not confirmed');return;}
  m.textContent='✓ Saved & applied';setTimeout(()=>{m.textContent='';refreshTelSipStatus();},1500);
 }catch(e){m.textContent=(saved?'Saved, but could not apply: ':'Could not save SIP settings: ')+e.message;}
 finally{if(button)button.disabled=false;}
}
`
