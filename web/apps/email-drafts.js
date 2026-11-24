// Persistent, account-scoped drafts. Saves are serialized; a stale response may
// acknowledge its own snapshot, but never replace newer text in the composer.
let draftState = null, draftSaveTimer = null, savedDrafts = [], draftListGeneration = 0;
const draftFields = ['to', 'cc', 'bcc', 'subject', 'body'];
const draftAttachmentLimit = 20 * 1024 * 1024;

function draftValues() {
  return Object.fromEntries(draftFields.map(key => [key, document.getElementById('c-' + key).value]));
}
function draftSnapshot() {
  return JSON.stringify({...draftValues(), uid:replyUid, folder:replyFolder,
    attachments:composeAtts.map(f => [f.name,f.size,f.lastModified])});
}
function draftIsOpen() { return document.getElementById('overlay').classList.contains('open'); }
function setDraftStatus(text, bad=false) {
  const el=document.getElementById('c-status');
  if(el){el.textContent=text;el.classList.toggle('save-error',bad);}
}
function resetDraftState(saved) {
  clearTimeout(draftSaveTimer);
  draftState={id:saved?.id || 0,revision:saved?.revision || 0,
    clientID:saved?.client_id || crypto.randomUUID(),generation:composeGeneration,
    saved:saved?draftSnapshot():'',attachmentsVersion:0,savedAttachmentsVersion:saved?0:-1,
    pending:null,conflict:false,blocked:saved?.status==='sending',mailboxChanged:false};
  setDraftStatus(saved?.status==='sending'?'Send not confirmed. Check Sent before sending a copy.':saved?'Saved in Prism':'Draft not saved yet',draftState.blocked);
  document.getElementById('draft-save-copy').hidden=!draftState.blocked;
  setComposerDisabled(draftState.blocked);
}
function draftHasContent() {
  const v=draftValues(),sig=cfg?.signature?.trim();
  return !!(v.to.trim()||v.cc.trim()||v.bcc.trim()||v.subject.trim()||composeAtts.length||
    (v.body.trim() && v.body.trim()!==('-- \n'+(sig||'')).trim()));
}
function scheduleDraftSave(attachmentsChanged=false) {
  if(!draftState||!draftIsOpen()||sendingMail)return;
  if(attachmentsChanged)draftState.attachmentsVersion++;
  publishMailContext();
  clearTimeout(draftSaveTimer);
  if(draftState.conflict||draftState.blocked)return;
  if(draftSnapshot()===draftState.saved){setDraftStatus('Saved in Prism');return;}
  if(!draftState.id&&!draftHasContent())return;
  setDraftStatus('Unsaved changes');
  draftSaveTimer=setTimeout(()=>flushDraft().catch(()=>{}),500);
}
async function attachmentJSON(file) {
  const data=await new Promise((resolve,reject)=>{
    const reader=new FileReader();reader.onerror=()=>reject(Error('Could not read attachment '+file.name));
    reader.onload=()=>resolve(String(reader.result).split(',')[1]||'');reader.readAsDataURL(file);
  });
  return {filename:file.name,content_type:file.type||'application/octet-stream',data};
}
async function flushDraft(options={}) {
  clearTimeout(draftSaveTimer);
  const state=draftState;
  if(!state||!draftIsOpen())return null;
  if(state.pending){await state.pending;if(draftState!==state)return null;return flushDraft(options);}
  if(state.conflict)throw Error('This draft changed elsewhere. Save your edits as a new draft or reopen the saved version.');
  if(state.blocked)throw Error('This draft is read-only. Check its mailbox and send status before saving a copy.');
  if(!state.id&&!draftHasContent())return null;
  const snapshot=draftSnapshot();
  if(snapshot===state.saved)return state;
  const values=draftValues(),attachmentVersion=state.attachmentsVersion;
  const payload={...values,id:state.id,revision:state.revision,client_id:state.clientID,uid:replyUid,folder:replyFolder};
  const files=composeAtts.slice();
  state.pending=(async()=>{
    setDraftStatus('Saving…');
    if(attachmentVersion!==state.savedAttachmentsVersion){
      if(files.reduce((n,f)=>n+f.size,0)>draftAttachmentLimit)throw Error('Attachments exceed the 20 MB draft limit. Remove a file to save.');
      payload.attachments=await Promise.all(files.map(attachmentJSON));
    }
    const response=await PrismAPI.api('/api/email/drafts',PrismAPI.body('POST',payload));
    const saved=response.draft;
    if(!saved?.id||!Number.isInteger(saved.revision))throw Error('The server did not confirm the saved draft.');
    if(draftState!==state)return;
    if(response.replayed){
      state.id=saved.id;state.revision=saved.revision;
      const error=Error('An earlier save was received. Reopen its saved version, or keep these edits as a new draft.');error.status=409;throw error;
    }
    if(draftFields.some(key=>(saved[key]||'')!==payload[key])){
      const error=Error('The saved draft has newer edits. Your local text is still here.');error.status=409;throw error;
    }
    state.id=saved.id;state.revision=saved.revision;state.saved=snapshot;state.savedAttachmentsVersion=attachmentVersion;
    setDraftStatus(draftSnapshot()===snapshot?'Saved in Prism':'Unsaved changes');
    loadSavedDrafts().catch(()=>{});
  })().catch(error=>{
    if(draftState===state){
      if(error.status===409||error.status===404){state.conflict=true;document.getElementById('draft-save-copy').hidden=false;}
      setDraftStatus('Not saved — '+error.message,true);
    }
    throw error;
  }).finally(()=>{state.pending=null;});
  await state.pending;
  if(draftState===state&&draftSnapshot()!==state.saved)return flushDraft(options);
  return state;
}
function setComposerDisabled(disabled) {
  for(const key of draftFields)document.getElementById('c-'+key).disabled=disabled;
  document.getElementById('c-file').disabled=disabled;
  document.getElementById('send-mail').disabled=disabled;
  document.getElementById('discard-draft').disabled=disabled;
  document.querySelectorAll('#c-atts .att-x').forEach(b=>b.disabled=disabled);
}
async function loadSavedDrafts() {
  const generation=++draftListGeneration;
  try{
    const result=await PrismAPI.api('/api/email/drafts');
    if(generation!==draftListGeneration)return;
    savedDrafts=result.drafts||[];
    const count=document.getElementById('draft-count');if(count)count.textContent=savedDrafts.length||'';
  }catch(error){
    const count=document.getElementById('draft-count');if(count)count.textContent='!';
    throw error;
  }
}
async function showSavedDrafts() {
  try{await loadSavedDrafts();}catch(error){PrismAPI.toast('Could not load drafts: '+error.message);return;}
  toggleFolders(false);
  const dialog=document.createElement('dialog');dialog.className='mail-dialog saved-drafts';
  dialog.innerHTML='<h3>Prism drafts</h3><p>Saved on this Prism server, separately from your mailbox’s Drafts folder.</p><div class="saved-draft-list"></div><div class="dialog-footer"><button class="btn" autofocus>Close</button></div>';
  const list=dialog.querySelector('.saved-draft-list');
  if(!savedDrafts.length){const empty=document.createElement('p');empty.textContent='No saved drafts.';list.append(empty);}
  for(const draft of savedDrafts){
    const button=document.createElement('button');button.className='saved-draft';
    const title=document.createElement('strong');title.textContent=draft.subject||'(no subject)';
    const recipients=document.createElement('span');recipients.textContent=draft.to||draft.cc||draft.bcc||'No recipient yet';
    const meta=document.createElement('small');meta.textContent=new Date(draft.updatedAt).toLocaleString()+
      (draft.attachmentCount?' · '+draft.attachmentCount+' attachment(s)':'')+(draft.status==='sending'?' · send not confirmed':'');
    button.append(title,recipients,meta);button.onclick=async()=>{
      if(draftIsOpen()&&!await closeCompose())return;
      try{await openSavedDraft(draft.id);dialog.close();}catch(error){PrismAPI.toast('Could not open draft: '+error.message);}
    };list.append(button);
  }
  dialog.querySelector('.dialog-footer button').onclick=()=>dialog.close();
  dialog.addEventListener('close',()=>dialog.remove());document.body.append(dialog);dialog.showModal();
}
async function openSavedDraft(id) {
  if(draftIsOpen())throw Error('Close the current draft first.');
  const generation=composeGeneration;
  const {draft,mailboxChanged}=await PrismAPI.api('/api/email/drafts?id='+encodeURIComponent(id));
  if(generation!==composeGeneration||draftIsOpen())throw Error('Another draft was opened while this one loaded.');
  const files=(draft.attachments||[]).map(a=>{
    const bytes=Uint8Array.from(atob(a.data||''),c=>c.charCodeAt(0));
    return new File([bytes],a.filename,{type:a.content_type||'application/octet-stream'});
  });
  replyUid=draft.uid||0;
  openCompose('Saved draft',draft.to,draft.subject,draft.body,draft.folder||'INBOX',draft.cc,draft.bcc,true);
  composeAtts=files;renderComposeAtts();resetDraftState(draft);
  if(mailboxChanged){
    draftState.blocked=true;draftState.mailboxChanged=true;
    setDraftStatus('This draft belongs to a different mailbox. Save a copy to use the current account.',true);
    document.getElementById('draft-save-copy').hidden=false;setComposerDisabled(true);
  }
  publishMailContext();
}
async function saveDraftCopy() {
  if(!draftState||sendingMail)return;
  const prior=draftState,generation=composeGeneration;
  const warning=draftState.blocked?'Save a copy for the currently connected mailbox? If an earlier send was interrupted, check Sent first to avoid sending the message twice.':'Keep these edits as a new draft? The existing saved version will remain unchanged.';
  if(!await PrismModal.confirm(warning,{okLabel:'Save as new draft'}))return;
  if(draftState!==prior||composeGeneration!==generation)return;
  if(prior.pending)await prior.pending.catch(()=>{});
  if(draftState!==prior||composeGeneration!==generation)return;
  // IMAP UIDs belong to one mailbox. A copy under a new account is a new
  // message, not a reply to whichever message happens to have the old UID.
  if(prior.mailboxChanged){replyUid=0;replyFolder='INBOX';}
  resetDraftState();
  try{await flushDraft();PrismAPI.toast('Saved as a new draft.','ok');}catch(error){document.getElementById('c-err').textContent=error.message;}
}
async function discardCompose() {
  if(sendingMail||!draftState||draftState.blocked)return;
  const state=draftState,generation=composeGeneration;
  if(!await PrismModal.confirm('Delete this draft and its attachments from Prism?',{danger:true,okLabel:'Delete draft'}))return;
  if(generation!==composeGeneration)return;
  clearTimeout(draftSaveTimer);
  try{
    if(state.pending)await state.pending;
    if(state.id)await PrismAPI.api('/api/email/drafts?id='+state.id+'&revision='+state.revision,{method:'DELETE'});
    await closeCompose(true);loadSavedDrafts().catch(()=>{});
  }catch(error){setDraftStatus('Draft not deleted — '+error.message,true);}
}
document.addEventListener('input',e=>{if(draftFields.some(k=>e.target.id==='c-'+k))scheduleDraftSave();});
addEventListener('beforeunload',e=>{
  if(sendingMail||(draftIsOpen()&&draftState&&(draftState.pending||draftHasContent()&&draftSnapshot()!==draftState.saved))){e.preventDefault();e.returnValue='';}
});
document.addEventListener('visibilitychange',()=>{if(document.hidden&&draftIsOpen())flushDraft().catch(()=>{});});
