// The mail UI uses the same executor as the agent for folder and rule actions.
let mailFolders=[];
function mailURL(path){return path+'?folder='+encodeURIComponent(folder);}
async function mailTool(action,args={}) {
 const d=await PrismAPI.api('/api/builtin/email?session='+encodeURIComponent(SESSION),PrismAPI.body('POST',{action,folder,...args}));
 if(d.error)throw new Error(d.error);
 if(typeof d.result==='string'){try{return JSON.parse(d.result);}catch(_){return d.result;}}return d.result;
}
async function loadFolders(){
 try {mailFolders=await mailTool('folders');renderFolderNav();}
 catch(e){const nav=document.getElementById('folder-nav');if(nav){nav.replaceChildren();const b=document.createElement('button');b.className='nav-item';b.textContent='Retry loading folders';b.onclick=loadFolders;nav.appendChild(b);}PrismAPI.toast(e.message);}
}
function changeFolder(name){folder=name;activeFilter='';current=null;messages=[];searchQuery='';unreadOnly=false;document.getElementById('search').value='';toggleFolders(false);renderFolderNav();loadInbox();}
function olderMail(){mailOffset+=inboxLimit();loadInbox(true);}
function mailDialog(title,body){
 const d=document.createElement('dialog');d.className='mail-dialog';d.setAttribute('aria-label',title);
 d.innerHTML='<h3 style="margin:0 0 14px">'+esc(title)+'</h3>'+body+'<div class="dialog-footer"><button class="btn close-dialog">Close</button></div>';
 d.querySelector('.close-dialog').onclick=()=>d.close();d.onclose=()=>d.remove();document.body.appendChild(d);d.showModal();return d;
}
// Keep confirmations in the dialog top layer, above the management dialog.
function mailConfirm(message){
 return new Promise(resolve=>{
  const d=mailDialog('Confirm deletion','<p>'+esc(message)+'</p><button class="btn confirm-delete" style="color:var(--red)">Delete</button>');
  let accepted=false;d.querySelector('.close-dialog').textContent='Cancel';
  d.querySelector('.confirm-delete').onclick=()=>{accepted=true;d.close();};
  d.onclose=()=>{d.remove();resolve(accepted);};
 });
}
function folderOptions(selected=''){return mailFolders.filter(f=>f.selectable).map(f=>'<option value="'+esc(f.name)+'"'+(f.name===selected?' selected':'')+'>'+esc(f.name)+'</option>').join('');}
async function manageFolders(selected=folder){
 await loadFolders();
 const d=mailDialog('Manage folders','<div class="folder-management"><label>Folder<select id="mf-folder">'+folderOptions(selected)+'</select></label><label>Rename to<input id="mf-name" aria-label="New name for selected folder"></label><div class="folder-ops"><button class="btn" data-op="rename_folder">Rename</button><button class="btn" data-op="delete_folder">Delete folder</button></div><p>Renaming updates rule destinations automatically. Only empty folders unused by rules can be deleted. Inbox and system folders are protected.</p><label class="folder-create">Create a folder<input id="mf-create" placeholder="For example, Invoices"></label><div><button class="btn btn-accent" data-op="create_folder">Create folder</button></div></div><p class="error" role="alert"></p>');
 const selection=d.querySelector('#mf-folder');
 const sync=()=>{d.querySelector('#mf-name').value=selection.value;const f=mailFolders.find(f=>f.name===selection.value);d.querySelector('[data-op="delete_folder"]').disabled=!!f?.role;d.querySelector('[data-op="rename_folder"]').disabled=f?.role==='inbox';};selection.onchange=sync;sync();
 for(const b of d.querySelectorAll('[data-op]'))b.onclick=async()=>{
  const old=selection.value,action=b.dataset.op,name=d.querySelector(action==='create_folder'?'#mf-create':'#mf-name').value.trim();
  const error=d.querySelector('.error');error.textContent='';
  if(action!=='delete_folder'&&!name){error.textContent='Enter a folder name.';return;}
  if(action==='delete_folder'&&!await mailConfirm('Delete empty folder '+old+'?'))return;
  b.disabled=true;
  try{const result=await mailTool(action,{folder:action==='create_folder'?name:old,target:name});if(folder===old&&action!=='create_folder')folder=action==='rename_folder'?(result.folder||name):'INBOX';await loadFolders();d.close();loadInbox();PrismAPI.toast(action==='create_folder'?'Folder created':action==='rename_folder'?'Folder renamed':'Folder deleted');}
  catch(e){error.textContent=e.message;}finally{b.disabled=false;}
 };
}
async function mailAction(action,target){
 if(!current)return;
 const uid=current.uid,source=folder;
 try{await mailTool(action,{uid,folder:source,target});if(folder===source)await loadInbox(true);notifyBadge();}
 catch(e){PrismAPI.toast(e.message);}
}
async function moveMessage(){
 if(!current)return;
 const uid=current.uid,source=folder;await loadFolders();
 const d=mailDialog('Move message','<select aria-label="Destination">'+folderOptions(mailFolders.find(f=>f.selectable&&f.name!==source)?.name)+'</select> <button class="btn move">Move</button><p class="error"></p>');
 d.querySelector('.move').onclick=async()=>{const button=d.querySelector('.move');button.disabled=true;try{await mailTool('move',{uid,folder:source,target:d.querySelector('select').value});d.close();if(folder===source)loadInbox(true);notifyBadge();}catch(e){d.querySelector('.error').textContent=e.message;}finally{button.disabled=false;}};
}
async function manageRules(){
 try{
 await loadFolders();const rules=await mailTool('rules');
 const d=mailDialog('Inbox rules','<p>Enabled rules run every minute while Prism is running, including existing matching inbox messages. Rules run in order. No AI call is used.</p><div class="rule-list"></div><button class="btn add-rule">＋ Rule</button> <button class="btn run-rules">Run enabled rules now</button><pre class="result" style="white-space:pre-wrap"></pre>');
 if(!rules.length)d.querySelector('.rule-list').innerHTML='<p>No rules yet. Create one to keep your inbox organized automatically.</p>';
 for(const r of rules){const row=document.createElement('div');row.style.cssText='display:flex;align-items:center;gap:8px;padding:10px 0;border-bottom:1px solid var(--border)';
 const text=document.createElement('span');text.style.flex='1';text.textContent=(r.enabled?'':'Paused · ')+r.name+' — '+r.field+' contains '+r.contains+' → '+(r.action==='move'?r.target:'mark read');row.appendChild(text);
 for(const [label,action] of [['Edit','edit'],[r.enabled?'Pause':'Enable','toggle'],['Delete','delete']]){const b=document.createElement('button');b.className='btn';b.textContent=label;row.appendChild(b);b.onclick=async()=>{try{if(action==='edit'){d.close();editRule(r);return;}if(action==='delete'){if(!await mailConfirm('Delete rule '+r.name+'?'))return;await mailTool('rule_delete',{name:r.name});}else{await mailTool('rule_save',{name:r.name,enabled:!r.enabled});}d.close();manageRules();}catch(e){d.querySelector('.result').textContent=e.message;}};}d.querySelector('.rule-list').appendChild(row);}
 d.querySelector('.add-rule').onclick=()=>{d.close();editRule();};
 d.querySelector('.run-rules').onclick=async()=>{try{const rs=await mailTool('rules_apply');d.querySelector('.result').textContent=ruleReport(rs)||'No enabled rules to run.';refreshMail();}catch(e){d.querySelector('.result').textContent=e.message;}};
 }catch(e){PrismAPI.toast(e.message);}
}
function ruleReport(rs){return rs.map(r=>r.name+': '+r.matched+' matched, '+r.applied+' applied'+(r.error?' — '+r.error:'')+(r.messages?'\n'+r.messages.map(m=>'  '+m.subject).join('\n'):'')).join('\n');}
function editRule(rule={}){
 const d=mailDialog(rule.name?'Edit rule':'New rule','<form style="display:grid;gap:10px"><label>Name <input name="name" required></label><label>Header <select name="rule_field"><option value="from">From</option><option value="to">To</option><option value="subject">Subject</option></select></label><label>Contains <input name="rule_contains" required></label><label>Action <select name="rule_action"><option value="move">Move to folder</option><option value="mark_read">Mark read</option></select></label><label class="destination">Folder <select name="target">'+folderOptions(rule.target||mailFolders.find(f=>f.selectable&&f.name!=='INBOX')?.name)+'</select></label><label><input name="enabled" type="checkbox"> Run automatically every minute (includes existing inbox mail)</label><div><button class="btn preview" type="button">Preview matches</button> <button class="btn btn-accent" type="submit">Save</button></div><pre class="result" style="white-space:pre-wrap;max-height:200px;overflow:auto"></pre></form>');
 const f=d.querySelector('form');for(const [k,v] of Object.entries({name:rule.name||'',rule_field:rule.field||'from',rule_contains:rule.contains||'',rule_action:rule.action||'move'}))f.elements[k].value=v;
 f.elements.name.readOnly=!!rule.name;f.elements.enabled.checked=rule.enabled??true;
 const toggle=()=>{d.querySelector('.destination').style.display=f.elements.rule_action.value==='move'?'':'none';};f.elements.rule_action.onchange=toggle;toggle();
 const args=()=>({name:f.elements.name.value.trim(),rule_field:f.elements.rule_field.value,rule_contains:f.elements.rule_contains.value.trim(),rule_action:f.elements.rule_action.value,target:f.elements.target.value,enabled:f.elements.enabled.checked});
 d.querySelector('.preview').onclick=async()=>{if(!f.reportValidity())return;try{d.querySelector('.result').textContent=ruleReport(await mailTool('rule_preview',args()));}catch(e){d.querySelector('.result').textContent=e.message;}};
 f.onsubmit=async e=>{e.preventDefault();const b=f.querySelector('[type=submit]');b.disabled=true;try{await mailTool('rule_save',args());d.close();manageRules();}catch(e){d.querySelector('.result').textContent=e.message;}finally{b.disabled=false;}};
}
