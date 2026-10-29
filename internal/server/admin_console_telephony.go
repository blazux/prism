package server

// Shared settings form for the Telephony app — the switchboard persona and greeting, the
// reserved "voice" knowledge base, the spoken phrases and dictionary, and the SIP
// trunk. It only appears when this Prism is docked with a Prism Vox stack
// (/api/platform → voxDocked). The app serves configuration to administrators.
//
// Note where each field is stored: the persona lives in Prism (/api/voice), while
// the greeting, phrases, dictionary, handling parameters, voices and trunk are
// written straight through to Vox (/api/vox/...). Vox's own database is the store
// — this pane is an editor, not a second copy.

// adminTelephonyPane is the pane markup, inserted after Apps.
const adminTelephonyPane = `    <div class="pane" data-pane="telephony"><h2>Telephony</h2><span class="pane-scope">Deployment</span>
<style>.tel-section{margin:16px 0;border:1px solid var(--border);border-radius:8px}.tel-section>summary{padding:14px;cursor:pointer;font-weight:600}.tel-section-body{padding:0 14px 16px}.tel-section-body h3{font-size:15px;margin:20px 0 8px}</style>
<div class="hint">Connected to PrismVox. Recognised callers use their Prism agent; unknown callers use the Vox switchboard. Outbound calls use the Vox outbound agent. Saved agent settings apply to new calls.</div>
<details class="tel-section" open><summary>Agents &amp; models</summary><div class="tel-section-body">
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
      <label>Knowledge base <span class="hint" style="margin:0">— the one collection the switchboard may read, and read out loud to strangers</span></label>
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
      <div class="row"><button class="primary" onclick="saveTelVoice()">Save switchboard</button><span id="tel-vmsg" class="hint" style="margin:0"></span></div>

<h3>PrismVox model — unknown callers &amp; outbound calls</h3><div id="tel-inbound-model"></div>
<div class="row"><button class="primary" onclick="saveCfgFields(TEL_INBOUND_MODEL,'tel-imsg')">Save PrismVox model</button><span id="tel-imsg" class="hint" role="status"></span></div><h3>Outbound instructions</h3><div class="hint">PrismVox runs outbound missions and conversations with transfer recipients, including when connected to Prism. The mission is added to these instructions for each outbound call.</div><div id="tel-outbound-model"></div>
<div class="row"><button class="primary" onclick="saveCfgFields(TEL_OUTBOUND_MODEL,'tel-omsg')">Save outbound instructions</button><span id="tel-omsg" class="hint" role="status"></span></div></div></details>
<details class="tel-section"><summary>Voice &amp; greeting</summary><div class="tel-section-body">
      <h2 style="margin-top:18px;font-size:15px">Voice &amp; inbound greetings</h2>
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
        <iframe id="tel-speech-providers" title="Speech providers" src="/api/vox/voice/editor" style="width:100%;height:220px;border:0;margin-top:8px" loading="lazy"></iframe>
      </details>
      <h2 style="margin-top:26px;font-size:15px">Spoken phrases</h2>
      <div class="hint">Messages used at specific stages of a call or as fallbacks when no response is generated. Keep <code>%s</code> placeholders for names and the reason for the call.</div>
      <div id="tel-phrases"></div>
      <div class="row"><button class="primary" onclick="savePhrases()">Save phrases</button><span id="tel-pmsg" class="hint" style="margin:0"></span></div>

      <h2 style="margin-top:26px;font-size:15px">Pronunciation</h2>
      <div class="hint">How the agent reads acronyms aloud. Comma-separated.</div>
      <div id="tel-dict"></div>
      <div class="row"><button class="primary" onclick="saveDict()">Save dictionary</button><span id="tel-dmsg" class="hint" style="margin:0"></span></div>

</div></details>
<details class="tel-section"><summary>Transfers &amp; directories</summary><div class="tel-section-body">
      <h2 style="margin-top:26px;font-size:15px">Transfer directory</h2>
      <div class="hint">Everyone who can receive a transferred call: approved accounts with a phone number on their profile. <b>Blind</b> puts the caller straight through. <b>Attended</b> asks their name and reason first, announces them, and lets the recipient decline. Each person sets this on their own profile; you can override it here.</div>
      <div id="tel-dir">Loading…</div>

      <h2 style="margin-top:26px;font-size:15px">Outbound directory</h2>
      <div class="hint">People and businesses the agent can <b>call</b> — the plumber, a supplier, a customer. Ask it in plain words ("appelle le plombier et prends rendez-vous") and it looks the number up here. These contacts receive outbound calls; they are not transfer destinations.</div>
      <div id="tel-out">Loading…</div>
      <div class="row" style="gap:6px;margin-top:8px">
        <input id="tel-outname" placeholder="Name, e.g. le plombier" style="flex:1">
        <input id="tel-outphone" placeholder="+596696…" style="width:170px">
        <button onclick="addOutContact()">Add</button>
        <span id="tel-outmsg" class="hint" style="margin:0"></span>
      </div>

      <h2 style="margin-top:26px;font-size:15px">Call handling</h2>
      <div class="hint">Limit the number of exchanges with a transfer recipient while the caller waits on hold.</div>
      <div id="tel-handling"></div>
      <div class="row"><button class="primary" onclick="saveHandling()">Save call handling</button><span id="tel-hmsg" class="hint" style="margin:0"></span></div>

</div></details>
<details class="tel-section"><summary>Phone connection</summary><div class="tel-section-body">
      <h2 style="margin-top:26px;font-size:15px">SIP trunk</h2>
      <div class="hint" id="tel-sipstatus">Loading status…</div>
      <label>Registrar (host)</label><input id="sip-registrar" style="width:100%">
      <label>Registrar IP</label><input id="sip-registrar_ip" style="width:100%">
      <div class="row" style="gap:12px">
        <div style="flex:1"><label>SIP username</label><input id="sip-username" style="width:100%"></div>
        <div style="flex:1"><label>Password</label><input id="sip-password" type="password" placeholder="leave empty = unchanged" autocomplete="new-password" style="width:100%"></div>
      </div>
      <div class="row" style="gap:12px">
        <div style="flex:1"><label>SIP domain</label><input id="sip-domain" style="width:100%"></div>
        <div style="flex:0 0 110px"><label>TLS port</label><input id="sip-tls_port" style="width:100%"></div>
      </div>
      <label>Outbound caller ID name</label><input id="sip-callerid_name" style="width:100%">
      <label>Transfer method</label><select id="sip-transfer_method" style="width:100%"><option value="bridge">bridge (Asterisk stays in the media path)</option><option value="refer">refer (SIP REFER to the softswitch)</option></select>
      <div class="row"><button id="tel-sip-save" class="primary" onclick="saveTelSip()">Save &amp; apply</button><span id="tel-smsg" class="hint" style="margin:0"></span></div>
      <div class="hint" style="margin-top:6px">"Save &amp; apply" re-registers the trunk without restarting Asterisk.</div>
</div></details>
    </div>
`

// adminTelephonyJS is the pane's script, inserted after the Logs block and before
// the Platform block.
const adminTelephonyJS = `// ── Telephony: switchboard persona (Prism /api/voice) + SIP trunk (proxied Vox /api/vox/sip) ──
const SIP_FIELDS=['registrar','registrar_ip','username','domain','tls_port','callerid_name','transfer_method'];
// The switchboard reads a dedicated, reserved RAG scope ("voice"); documents are
// managed right here, so it's independent from any group.
// One collection, chosen — not "whatever sits in a scope". The two origins are in
// one list because it is one decision, but each option says which it is: what the
// switchboard reads, an unknown caller can hear.
async function loadVoiceKB(){
 const sel=$('tel-kbsel');if(!sel)return;
 const d=await jget('/api/voice/kb');const choices=(d&&d.choices)||[];
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
 await deleteVoiceKB(label);
 await fetch('/api/voice/kb',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({collection:''})});
 loadVoiceKB();
}
async function saveVoiceKBChoice(){
 const m=$('tel-kbmsg');m.textContent='Saving…';
 const r=await fetch('/api/voice/kb',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({collection:$('tel-kbsel').value})});
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
 let r;try{r=await fetch('/api/rag/upload?scope=voice',{method:'POST',body:fd});}catch(e){m.textContent='Failed: '+e.message;return;}
 m.textContent=r.ok?'✓ Added — select it above to put it on the line':'Failed ('+r.status+')';
 $('tel-kbfile').value='';$('tel-kbfilename').textContent='';
 loadVoiceKB();setTimeout(()=>m.textContent='',4000);
}
async function deleteVoiceKB(name){
 if(!await PrismModal.confirm('Delete "'+name+'" and all its documents?',{danger:true}))return;
 await fetch('/api/rag/collections?scope=voice&name='+encodeURIComponent(name),{method:'DELETE'});loadVoiceKB();
}
window.addEventListener('message',event=>{
 const frame=$('tel-speech-providers');
 if(event.origin!==location.origin||event.source!==frame?.contentWindow)return;
 if(event.data?.type==='prismconnect-voice-height')frame.style.height=Math.min(900,Math.max(160,event.data.height))+'px';
 if(event.data?.type==='prismconnect-voice-saved')loadTelVoiceGreeting();
});
async function loadTelephony(){
 const v=await jget('/api/voice');
 if(v){$('tel-persona').value=v.personality||'';loadVoiceModel(v.model||'');}
 loadVoiceKB();
 loadTelDirectory();       // who can receive a transfer, and how
 loadOutContacts();        // who the agent can call by name
 loadTelVoiceGreeting();   // voice list + clone controls (needs the TTS backend)
 loadPhoneCfg();           // greeting + phrases + dictionary + call handling
 const s=await jget('/api/vox/sip');
 if(s){SIP_FIELDS.forEach(f=>{if($('sip-'+f))$('sip-'+f).value=s['sip_'+f]||'';});}
 const st=await jget('/api/vox/sip/status');
 $('tel-sipstatus').innerHTML=(st&&st.endpoint_state==='online')?'✅ Trunk <b>online</b> — the number rings.':(st?'⚠️ Trunk <b>'+esc(st.endpoint_state||'?')+'</b> — check the config.':'Status unavailable.');
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
  const r=await fetch('/api/voice',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({model:$('tel-model').value}),signal:AbortSignal.timeout(15000)});
  if(!r.ok){const d=await r.json();throw new Error(d.error||'Could not save the phone model.');}
  msg.textContent='Saved · applies to new calls';
 }catch(e){msg.textContent=e.message;}finally{button.disabled=false;}
}
async function saveTelVoice(){
 const m=$('tel-vmsg');m.textContent='Saving…';
 const r=await fetch('/api/voice',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({personality:$('tel-persona').value})});
 m.textContent=r.ok?'✓ Saved':'Failed';setTimeout(()=>m.textContent='',2500);
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
  const rv=await fetch('/api/vox/tts/voice',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({name})});
  if(!rv.ok){m.textContent='Voice failed ('+rv.status+')';return;}
 }
 const rc=await fetch('/api/vox/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({values:{
  greeting:$('tel-greeting').value,
  greeting_known:$('tel-greeting-known').value}})});
 m.textContent=rc.ok?'✓ Saved':'Greeting failed ('+rc.status+')';
 setTimeout(()=>m.textContent='',2500);
}
async function previewVoice(){
 const m=$('tel-gmsg');const name=$('tel-voice').value;
 if(!name){m.textContent='Pick a voice first.';return;}
 m.textContent='Synthesizing…';
 // Preview always speaks the *current* voice, so select it first — otherwise you'd
 // hear the previously saved one and think the picker did nothing.
 await fetch('/api/vox/tts/voice',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({name})});
 const text=$('tel-greeting').value.trim()||'Bonjour, vous êtes bien au standard. Que puis-je faire pour vous ?';
 let r;try{r=await fetch('/api/vox/tts/preview',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text})});}
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
 let r;try{r=await fetch('/api/vox/tts/voices',{method:'POST',body:fd});}catch(e){m.textContent='Failed: '+e.message;return;}
 if(!r.ok){m.textContent='Failed ('+r.status+') — the API key may lack voice-write permission.';return;}
 m.textContent='✓ Voice cloned';
 $('tel-clonefile').value='';$('tel-clonefilename').textContent='';$('tel-clonename').value='';
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
 ['whisper_hotwords','Words to transcribe correctly','Heard, not spoken: biases transcription so it stops hearing "Shodan" as "je donne". Put people\'s names here above all — a name misheard is a transfer that fails.','in'],
 ['tts_spell_words','Always spell out','Read letter by letter, e.g. "IP" → "i pé".','in'],
 ['tts_spell_exceptions','Never spell out','Read as a word despite looking like an acronym, e.g. "OK".','in'],
];
const TEL_HANDLING=[
 ['transfer_dialog_turns','Max exchanges with a transfer recipient','1–10. Empty uses 3.','num'],
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
  const field=kind==='ta'?'<textarea id="cf-'+k+'" style="min-height:44px">'+v+'</textarea>'
    :'<input id="cf-'+k+'"'+(kind==='num'?' type="number" min="1" max="10"':kind==='url'?' type="url"':'')+' value="'+v+'" style="width:100%">';
  const markup = '<label style="margin-top:8px">'+label+'</label><div class="hint" style="margin:0 0 3px">'+hint+'</div>'+field;
  return kind==='url'?'<details style="margin-top:10px"><summary>Advanced: model endpoint</summary>'+markup+'</details>':markup;
 }).join('');
}
async function loadPhoneCfg(){
 TEL_CFG=(await jget('/api/vox/config'))||{};
 renderCfgFields('tel-phrases',TEL_PHRASES);
 renderCfgFields('tel-dict',TEL_DICT);
 renderCfgFields('tel-handling',TEL_HANDLING);
 renderCfgFields('tel-inbound-model',TEL_INBOUND_MODEL);
 renderCfgFields('tel-outbound-model',TEL_OUTBOUND_MODEL);
 $('tel-greeting').value=TEL_CFG.greeting||'';
 // Empty in the config table = the compiled-in default is in force. Showing it
 // rather than an empty box is what makes the field editable instead of a
 // mystery: an admin sees the sentence their callers actually hear.
 $('tel-greeting-known').value=TEL_CFG.greeting_known||DEFAULT_GREETING_KNOWN;
}
const DEFAULT_GREETING_KNOWN="Bonjour %s, c'est votre assistant. Que puis-je faire pour vous ?";
async function saveCfgFields(spec,msgId){
 const m=$(msgId);m.textContent='Saving…';
 const values={};spec.forEach(([k])=>{const el=$('cf-'+k);if(el)values[k]=el.value;});
 const r=await fetch('/api/vox/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({values})});
 m.textContent=r.ok?'✓ Saved':'Failed ('+r.status+')';
 if(r.ok)Object.assign(TEL_CFG,values);
 setTimeout(()=>m.textContent='',2500);
}
// The transfer directory is Prism's own user list, not a telephony table: being
// transferable means having an account here with a number on it. Shown where an
// admin thinks about transfers rather than buried in the Users tab, which does
// not display phone numbers at all.
async function loadTelDirectory(){
 const box=$('tel-dir');if(!box)return;
 const d=await jget('/api/voice/directory');const rows=(d&&d.entries)||[];
 if(!rows.length){box.innerHTML='<div class="hint">Nobody has a phone number on their profile yet, so no call can be transferred. Add one in Settings → Profile.</div>';return;}
 box.innerHTML='<table><tr><th>Name</th><th>Number</th><th>Transfer</th></tr>'+rows.map(e=>{
  const att=e.transfer==='attended';
  return '<tr><td>'+esc(e.name)+'</td><td>'+esc(e.phone)+'</td><td>'+
   '<select onchange="setTelTransfer('+e.id+',this.value)">'+
   '<option value="blind"'+(att?'':' selected')+'>Blind — put straight through</option>'+
   '<option value="attended"'+(att?' selected':'')+'>Attended — announce first</option>'+
   '</select></td></tr>';}).join('')+'</table>';
}
async function setTelTransfer(id,kind){
 const r=await jpost('/api/admin/users',{id,action:kind==='attended'?'transfer_attended':'transfer_blind'});
 if(!r.ok)alert('Could not save: HTTP '+r.status);
 loadTelDirectory();
}

// The outbound directory lives in Vox — it is telephony data, and Vox resolves
// the name when a call is placed. Edited here because Prism is the only console.
async function loadOutContacts(){
 const box=$('tel-out');if(!box)return;
 const d=await jget('/api/vox/contacts');const rows=Array.isArray(d)?d:((d&&d.items)||[]);
 if(!rows.length){box.innerHTML='<div class="hint">Nobody yet. Add a contact below and the agent can call them by name.</div>';return;}
 box.innerHTML='<table><tr><th>Name</th><th>Number</th><th></th></tr>'+rows.map(c=>
  '<tr><td>'+esc(c.name)+'</td><td>'+esc(c.phone_number||'')+'</td>'+
  '<td style="text-align:right"><button onclick="rmOutContact('+c.id+',\''+esc(c.name).replace(/'/g,"\\'")+'\')">Remove</button></td></tr>').join('')+'</table>';
}
async function addOutContact(){
 const m=$('tel-outmsg');const name=$('tel-outname').value.trim();const phone=$('tel-outphone').value.trim();
 if(!name||!phone){m.textContent='Name and number required';return;}
 m.textContent='Saving…';
 const r=await fetch('/api/vox/contacts',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name,phone_number:phone})});
 if(r.ok){$('tel-outname').value='';$('tel-outphone').value='';m.textContent='✓ Added';setTimeout(()=>m.textContent='',1500);loadOutContacts();}
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
  const r=await fetch('/api/vox/sip',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  if(!r.ok){m.textContent=await telephonyResponseError(r,'Could not save SIP settings');return;}
  saved=true;$('sip-password').value='';m.textContent='Saved. Applying…';
  const applied=await fetch('/api/vox/sip/apply',{method:'POST'});
  if(!applied.ok){m.textContent='Saved, but could not apply: '+await telephonyResponseError(applied,'Reload failed');return;}
  const result=await applied.json();
  if(result.ok!==true){m.textContent='Saved, but could not apply: '+(result.detail||'Reload not confirmed');return;}
  m.textContent='✓ Saved & applied';setTimeout(()=>{m.textContent='';loadTelephony();},1500);
 }catch(e){m.textContent=(saved?'Saved, but could not apply: ':'Could not save SIP settings: ')+e.message;}
 finally{if(button)button.disabled=false;}
}
`
