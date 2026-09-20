package server

// Telephony pane of the admin console — the switchboard persona and greeting, the
// reserved "voice" knowledge base, the spoken phrases and dictionary, and the SIP
// trunk. It only appears when this Prism is docked with a Prism Vox stack
// (/api/platform → voxDocked); see admin_console_page.go for how the page is
// assembled.
//
// Note where each field is stored: the persona lives in Prism (/api/voice), while
// the greeting, phrases, dictionary, handling parameters, voices and trunk are
// written straight through to Vox (/api/vox/...). Vox's own database is the store
// — this pane is an editor, not a second copy.

// adminTelephonyPane is the pane markup, inserted after Apps.
const adminTelephonyPane = `    <div class="pane" data-pane="telephony"><h2>Telephony</h2><div class="hint">This deployment is docked with a Prism Vox phone stack. The agent also answers the phone: a known caller (number on their profile) gets their own agent, an unknown one gets the switchboard configured here.</div>

      <div class="hint" style="margin-top:10px">Everything here is stored by the phone stack itself — this page writes to it directly, so there is no second copy to keep in sync. <b>Placing calls and watching activity are not forms:</b> ask the agent ("appelle le plombier et prends rendez-vous", "quels appels sont en attente ?") or build a widget against <code>/api/vox/…</code>, which any admin session can reach.</div>

      <h2 style="margin-top:18px;font-size:15px">Voice &amp; greeting — every call</h2>
      <div class="hint">How the agent sounds, and the first thing it says when it picks up. This applies to <em>every</em> caller — known or not — so it sits above the switchboard, which only shapes what it says to strangers.</div>
      <div class="row" style="gap:12px;align-items:flex-end">
        <div style="flex:1"><label>Voice</label><select id="tel-voice" style="width:100%"><option>Loading…</option></select></div>
        <button class="mini" onclick="previewVoice()">Preview</button>
      </div>
      <div id="tel-voicehint" class="hint" style="margin-top:4px"></div>
      <label style="margin-top:10px">Greeting — unknown caller <span class="hint" style="margin:0">— spoken on pickup, before the agent thinks at all</span></label>
      <textarea id="tel-greeting" placeholder="Loading…" style="min-height:60px"></textarea>
      <label style="margin-top:10px">Greeting — someone we recognise <span class="hint" style="margin:0">— when the number matches a profile. <code>%s</code> = their name; leave it out and the line is spoken as written.</span></label>
      <textarea id="tel-greeting-known" placeholder="Loading…" style="min-height:60px"></textarea>
      <div class="hint" style="margin-top:4px">Two greetings because the caller is told who picked up before a single word is generated: a colleague should not hear "you have reached the switchboard". This one also tells you, on the very first second of a test call, which brain took it.</div>
      <div class="row" style="gap:8px;margin-top:6px;align-items:center" id="tel-clonebox">
        <span class="filebtn"><input type="file" id="tel-clonefile" accept="audio/*" onchange="showPicked('tel-clonefile','tel-clonefilename')"><button type="button" onclick="document.getElementById('tel-clonefile').click()">Choose a voice sample…</button></span>
        <span id="tel-clonefilename" class="filename"></span>
        <input id="tel-clonename" placeholder="New voice name" style="width:170px">
        <button class="primary" onclick="cloneVoice()">Clone voice</button>
      </div>
      <div class="row"><button class="primary" onclick="saveTelVoiceGreeting()">Save voice &amp; greeting</button><span id="tel-gmsg" class="hint" style="margin:0"></span></div>

      <h2 style="margin-top:26px;font-size:15px">Switchboard — unknown callers</h2>
      <div class="hint">Role and tone for a caller the agent doesn't recognise. Never any access to tools/files/personal data.</div>
      <label>Personality</label><textarea id="tel-persona" placeholder="Loading…" style="min-height:150px"></textarea>
      <label>Knowledge base <span class="hint" style="margin:0">— the one collection the switchboard may read, and read out loud to strangers</span></label>
      <select id="tel-kbsel" style="width:100%" onchange="saveVoiceKBChoice()"><option>Loading…</option></select>
      <div class="row" style="gap:8px;margin-top:4px;align-items:center">
        <span id="tel-kbmsg" class="hint" style="margin:0"></span>
        <button id="tel-kbdel" class="mini" style="display:none" onclick="deleteSelectedVoiceKB()">Delete this collection</button>
      </div>
      <div class="hint" style="margin-top:8px">Pick an existing collection — group collections are the ones you upload to as usual in RAG, so the same documents serve your members and the switchboard. <b>Anything you point at here is readable by unknown callers</b>, so a group collection is a deliberate choice, not a shortcut.</div>
      <div class="hint" style="margin-top:8px">Or keep a base that belongs to the switchboard alone and to nobody else:</div>
      <div class="row" style="gap:8px;margin-top:4px;align-items:center">
        <span class="filebtn"><input type="file" id="tel-kbfile" accept=".pdf,.txt,.md,.docx,.html,.csv" onchange="showPicked('tel-kbfile','tel-kbfilename')"><button type="button" onclick="document.getElementById('tel-kbfile').click()">Choose a file…</button></span>
        <span id="tel-kbfilename" class="filename"></span>
        <input id="tel-kbcol" placeholder="Collection name" style="width:150px">
        <button class="primary" onclick="uploadVoiceKB()">Upload</button>
        <span id="tel-kbupmsg" class="hint" style="margin:0"></span>
      </div>
      <div class="row"><button class="primary" onclick="saveTelVoice()">Save switchboard</button><span id="tel-vmsg" class="hint" style="margin:0"></span></div>

      <h2 style="margin-top:26px;font-size:15px">Transfer directory</h2>
      <div class="hint">Everyone who can receive a transferred call: approved accounts with a phone number on their profile. Nobody else is transferable — that is the rule, not a limitation. <b>Blind</b> puts the caller straight through. <b>Attended</b> asks their name and reason first, announces them, and lets the recipient decline. Each person sets this on their own profile; you can override it here.</div>
      <div id="tel-dir">Loading…</div>

      <h2 style="margin-top:26px;font-size:15px">Outbound directory</h2>
      <div class="hint">People and businesses the agent can <b>call</b> — the plumber, a supplier, a customer. Ask it in plain words ("appelle le plombier et prends rendez-vous") and it looks the number up here. Separate from the transfer directory above on purpose: these contacts receive calls, they never receive transfers.</div>
      <div id="tel-out">Loading…</div>
      <div class="row" style="gap:6px;margin-top:8px">
        <input id="tel-outname" placeholder="Name, e.g. le plombier" style="flex:1">
        <input id="tel-outphone" placeholder="+596696…" style="width:170px">
        <button onclick="addOutContact()">Add</button>
        <span id="tel-outmsg" class="hint" style="margin:0"></span>
      </div>

      <h2 style="margin-top:26px;font-size:15px">Spoken phrases</h2>
      <div class="hint">Fixed lines the call itself speaks, in French — not the agent improvising. Three of them are <b>always</b> used (hold, transfer, connecting): they cover network latency right before an irreversible action, where a hallucinated name would betray the caller. The others are fallbacks the agent normally supersedes. <code>%s</code> is a placeholder — keep it.</div>
      <div id="tel-phrases"></div>
      <div class="row"><button class="primary" onclick="savePhrases()">Save phrases</button><span id="tel-pmsg" class="hint" style="margin:0"></span></div>

      <h2 style="margin-top:26px;font-size:15px">Pronunciation</h2>
      <div class="hint">How the agent reads acronyms aloud. Comma-separated.</div>
      <div id="tel-dict"></div>
      <div class="row"><button class="primary" onclick="saveDict()">Save dictionary</button><span id="tel-dmsg" class="hint" style="margin:0"></span></div>

      <h2 style="margin-top:26px;font-size:15px">Call handling</h2>
      <div class="hint">The phone stack runs a few micro-tasks on its own model — re-prompting a silent caller, announcing a transfer, judging whether the recipient accepted, summarising the call. The <em>conversation</em> itself is this agent's brain; these are not.</div>
      <div id="tel-handling"></div>
      <div class="row"><button class="primary" onclick="saveHandling()">Save call handling</button><span id="tel-hmsg" class="hint" style="margin:0"></span></div>

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
      <div class="row"><button class="primary" onclick="saveTelSip()">Save &amp; apply</button><span id="tel-smsg" class="hint" style="margin:0"></span></div>
      <div class="hint" style="margin-top:6px">"Save &amp; apply" re-registers the trunk without restarting Asterisk. Detailed voices/phrases/dictionary config still lives in the Vox interface for now.</div>
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
async function loadTelephony(){
 const v=await jget('/api/voice');
 if(v)$('tel-persona').value=v.personality||'';
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
   : 'The local voice engine ('+((b&&b.backend)||'local')+') has a single fixed voice — switch to ElevenLabs to pick or clone one.';
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
 ['transfer_dialog_turns','Max exchanges with a transfer recipient','1–10. The caller waits on hold meanwhile; a noisy line may need more.','num'],
 ['llm_model_standard','Micro-task model — inbound','Model alias on the LLM gateway.','in'],
 ['llm_model_oncall','Micro-task model — outbound missions','Model alias on the LLM gateway.','in'],
];
let TEL_CFG={};
function renderCfgFields(boxId,spec){
 $(boxId).innerHTML=spec.map(([k,label,hint,kind])=>{
  const v=esc(TEL_CFG[k]||'');
  const field=kind==='ta'?'<textarea id="cf-'+k+'" style="min-height:44px">'+v+'</textarea>'
    :'<input id="cf-'+k+'"'+(kind==='num'?' type="number" min="1" max="10"':'')+' value="'+v+'" style="width:100%">';
  return '<label style="margin-top:8px">'+label+'</label><div class="hint" style="margin:0 0 3px">'+hint+'</div>'+field;
 }).join('');
}
async function loadPhoneCfg(){
 TEL_CFG=(await jget('/api/vox/config'))||{};
 renderCfgFields('tel-phrases',TEL_PHRASES);
 renderCfgFields('tel-dict',TEL_DICT);
 renderCfgFields('tel-handling',TEL_HANDLING);
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

async function saveTelSip(){
 const m=$('tel-smsg');m.textContent='Applying…';
 const body={};SIP_FIELDS.forEach(f=>body['sip_'+f]=$('sip-'+f)?$('sip-'+f).value.trim():'');
 const pw=$('sip-password').value;if(pw)body.sip_password=pw;
 const r=await fetch('/api/vox/sip',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
 if(r.ok){await fetch('/api/vox/sip/apply',{method:'POST'});m.textContent='✓ Saved & applied';$('sip-password').value='';setTimeout(()=>{m.textContent='';loadTelephony();},1500);}
 else m.textContent='Failed ('+r.status+')';
}
`
