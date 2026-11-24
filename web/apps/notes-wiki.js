let wikiNavigation=[],wikiNavigationIndex=-1;
const wikiNormalize=value=>String(value||'').trim().normalize('NFC').toLowerCase();
function rebaseCanonicalNoteBody(submitted,canonical,current) {
  if(current===submitted)return canonical;
  if(canonical===submitted)return current;
  const before=submitted.split('\n'),after=canonical.split('\n');
  if(before.length!==after.length||before[0].trim()!=='---')return current;
  const changed=before.map((line,index)=>line!==after[index]?index:-1).filter(index=>index>=0);
  const end=before.findIndex((line,index)=>index>0&&line.trim()==='---');
  if(changed.length!==1||changed[0]<=0||changed[0]>=end||!/^\s*title\s*:/i.test(before[changed[0]]))return current;
  const lines=current.split('\n');if(lines[0].trim()!=='---')return current;
  const last=lines.findIndex((line,index)=>index>0&&line.trim()==='---');
  let title=-1;for(let index=1;index<last;index++)if(/^\s*title\s*:/i.test(lines[index]))title=index;
  if(title>=0&&lines[title]===before[changed[0]])lines[title]=after[changed[0]];
  return lines.join('\n');
}
function wikiCandidates(target,scope=currentNote()?.scope||'personal') {
  const exact=notes.filter(n=>String(n.id)===target);
  if(exact.length)return exact;
  const key=wikiNormalize(target),withoutExtension=key.replace(/\.md$/,'');
  return notes.filter(n=>(n.scope||'personal')===scope&&[n.title,...(n.aliases||[]),n.path].filter(Boolean)
    .some(value=>{const name=wikiNormalize(value);return name===key||name.replace(/\.md$/,'')===withoutExtension;}));
}
function wikiParts(value) {
  const split=value.indexOf('|');
  return {target:(split<0?value:value.slice(0,split)).trim(),label:(split<0?value:value.slice(split+1)).trim()};
}
// Markdown tokenization keeps code examples out of the link index.
function wikiTargets(src) {
  if(!window.marked)return [];
  const targets=[];
  function visit(token){
    if(!token||['code','codespan','html','image'].includes(token.type))return;
    if(token.type==='link'){
      const view=PrismLinks.parse(token.href);
      if(view?.type==='app'&&view.name==='notes'&&view.id)targets.push(view.id);
      return;
    }
    if(token.tokens){token.tokens.forEach(visit);return;}
    if(token.items){token.items.forEach(visit);return;}
    if(token.type==='text')for(const match of (token.text||'').matchAll(/\[\[([^\]\n]+)\]\]/g)){
      const {target}=wikiParts(match[1]);if(target)targets.push(target);
    }
  }
  marked.lexer(src||'').forEach(visit);
  return targets;
}
function renderMarkdown(src) {
  if(!window.marked)return esc(src||'');
  const container=document.createElement('div');
  const parsed=marked.parse(src||'');
  container.innerHTML=window.DOMPurify?DOMPurify.sanitize(parsed):esc(src||'');
  const walker=document.createTreeWalker(container,NodeFilter.SHOW_TEXT),text=[];
  while(walker.nextNode())if(!walker.currentNode.parentElement.closest('code,pre,a,script,style'))text.push(walker.currentNode);
  for(const node of text){
    const matches=[...node.textContent.matchAll(/\[\[([^\]\n]+)\]\]/g)];if(!matches.length)continue;
    const fragment=document.createDocumentFragment();let at=0;
    for(const match of matches){
      fragment.append(document.createTextNode(node.textContent.slice(at,match.index)));
      const {target,label}=wikiParts(match[1]),candidates=wikiCandidates(target),anchor=document.createElement('a');
      anchor.className='wikilink'+(!candidates.length?' missing':candidates.length>1?' ambiguous':'');
      anchor.href='#';anchor.dataset.note=target;anchor.textContent=label||target;
      if(candidates.length>1)anchor.title='Several pages match. Choose which one to open.';
      fragment.append(anchor);at=match.index+match[0].length;
    }
    fragment.append(document.createTextNode(node.textContent.slice(at)));node.replaceWith(fragment);
  }
  return container.innerHTML;
}
function wikiDialog(title,intro='') {
  const dialog=document.createElement('dialog');dialog.className='wiki-dialog';
  const heading=document.createElement('h2');heading.textContent=title;dialog.append(heading);
  if(intro){const help=document.createElement('p');help.textContent=intro;dialog.append(help);}
  const content=document.createElement('div');content.className='wiki-dialog-content';dialog.append(content);
  const error=document.createElement('p');error.className='wiki-error';error.setAttribute('role','status');dialog.append(error);
  const footer=document.createElement('div');footer.className='wiki-dialog-footer';
  const close=document.createElement('button');close.className='btn';close.textContent='Close';close.onclick=()=>dialog.close();footer.append(close);dialog.append(footer);
  dialog.addEventListener('close',()=>dialog.remove());document.body.append(dialog);dialog.showModal();
  return {dialog,content,error,footer};
}
function chooseWikiPage(candidates,title='Choose a page') {
  return new Promise(resolve=>{
    const ui=wikiDialog(title),list=document.createElement('div');list.className='wiki-page-list';ui.content.append(list);
    if(candidates.length>8){
      const search=document.createElement('input');search.type='search';search.placeholder='Find a page…';search.setAttribute('aria-label','Find a page');search.className='wiki-page-search';ui.content.prepend(search);
      search.oninput=()=>{const query=wikiNormalize(search.value);list.querySelectorAll('.wiki-page-option').forEach(button=>button.hidden=!!query&&!button.dataset.search.includes(query));};
      search.focus();
    }
    let selected=null;
    for(const note of candidates){
      const button=document.createElement('button');button.className='wiki-page-option';
      button.dataset.search=wikiNormalize([note.title,note.id,...(note.aliases||[])].join(' '));
      const name=document.createElement('strong');name.textContent=note.title||'(untitled)';
      const location=document.createElement('small');location.textContent=(note.scope==='group'?'Group publication':'Personal')+' · '+note.id;
      button.append(name,location);button.onclick=()=>{selected=note;ui.dialog.close();};list.append(button);
    }
    ui.dialog.addEventListener('close',()=>resolve(selected));
  });
}
async function openWikiLink(target) {
  const candidates=wikiCandidates(target);
  const match=candidates.length===1?candidates[0]:candidates.length>1?await chooseWikiPage(candidates,'Several pages match “'+target+'”'):null;
  if(match){await select(match.id);return;}
  if(candidates.length)return;
  if(!await saveNote())return;
  if(await PrismModal.confirm('Page “'+target+'” does not exist yet. Create it?',{okLabel:'Create page'})){
    try{
      const {id}=await api('?session='+encodeURIComponent(session),PrismAPI.body('POST',{title:target,body:'',tags:''}));
      await load();await select(id);
    }catch(error){PrismAPI.toast('Could not create the page: '+error.message);}
  }
}
function wikiRemember(id) {
  if(wikiNavigation[wikiNavigationIndex]===id){updateWikiNavigation();return;}
  wikiNavigation=wikiNavigation.slice(0,wikiNavigationIndex+1);wikiNavigation.push(id);
  if(wikiNavigation.length>100)wikiNavigation.shift();wikiNavigationIndex=wikiNavigation.length-1;updateWikiNavigation();
}
function wikiRenameID(oldID,newID) {
  wikiNavigation=wikiNavigation.map(id=>id===oldID?newID:id);
}
function updateWikiNavigation() {
  const back=$('wiki-back'),forward=$('wiki-forward');if(!back||!forward)return;
  back.disabled=wikiNavigationIndex<=0;forward.disabled=wikiNavigationIndex>=wikiNavigation.length-1;
}
async function navigateWiki(delta) {
  const next=wikiNavigationIndex+delta;if(next<0||next>=wikiNavigation.length)return;
  const id=wikiNavigation[next];
  if(!notes.some(n=>n.id===id)){PrismAPI.toast('This page is unavailable.');return;}
  if(await select(id,false)){wikiNavigationIndex=next;updateWikiNavigation();}
}
function updateWikiOutline() {
  const host=$('wiki-outline');if(!host)return;host.replaceChildren();
  if(!selectedId||!window.marked)return;
  let offset=0;const headings=[];
  for(const token of marked.lexer($('e-body').value||'')){
    if(token.type==='heading')headings.push({...token,offset});offset+=(token.raw||'').length;
  }
  for(const [index,heading] of headings.entries()){
    const button=document.createElement('button');button.className='wiki-outline-item';
    button.style.paddingLeft=(8+(heading.depth-1)*10)+'px';button.textContent=heading.text.replace(/[*_`]/g,'');
    button.onclick=()=>{
      if(mode!=='edit'){
        const target=$('e-preview').querySelectorAll('h1,h2,h3,h4,h5,h6')[index];target?.scrollIntoView({block:'start'});
      }else{
        const ta=$('e-body');ta.focus();ta.setSelectionRange(heading.offset,heading.offset);ta.scrollTop=Math.max(0,(ta.value.slice(0,heading.offset).split('\n').length-2)*22);
      }
    };host.append(button);
  }
  if(!headings.length)host.textContent='Add Markdown headings to build an outline.';
}
function updateWikiDetails() {
  updateWikiOutline();const host=$('wiki-backlinks');if(!host)return;host.replaceChildren();
  const current=notes.find(n=>n.id===selectedId);if(!current)return;
  const incoming=notes.filter(n=>n.id!==selectedId&&wikiTargets(n.body).some(target=>{
    const candidates=wikiCandidates(target,n.scope||'personal');return candidates.length===1&&candidates[0].id===selectedId;
  }));
  $('wiki-backlink-count').textContent=incoming.length?' · '+incoming.length+' linked page(s)':'';
  for(const note of incoming){const button=document.createElement('button');button.className='wiki-outline-item';button.textContent=note.title||'(untitled)';button.onclick=()=>select(note.id);host.append(button);}
  if(!incoming.length)host.textContent='No other page links here yet.';
}
async function insertWikiLink() {
  if(!selectedId||$('e-body').readOnly)return;
  const noteID=selectedId,version=aiEditVersion,ta=$('e-body'),start=ta.selectionStart,end=ta.selectionEnd;
  const match=await chooseWikiPage(notes.filter(n=>n.id!==noteID),'Insert a page link');
  if(!match)return;
  if(selectedId!==noteID||aiEditVersion!==version||ta.readOnly){PrismAPI.toast('The editor changed. Insert the link again.');return;}
  const title=(match.title||'Untitled').replace(/[\[\]\\]/g,'\\$&');
  const link='['+title+'](/?app=notes&id='+encodeURIComponent(match.id)+')';
  ta.value=ta.value.slice(0,start)+link+ta.value.slice(end);ta.setSelectionRange(start+link.length,start+link.length);
  aiEditVersion++;publishNoteContext();if(mode!=='edit')updatePreview();updateWikiDetails();await saveNote();
}
async function showNoteHistory() {
  const note=currentNote();if(!note||note.scope==='group'&&!note.canEdit)return;
  if(!await saveNote())return;
  const noteID=selectedId,scope=note.scope||'personal';
  const ui=wikiDialog('Version history','Restore a previous version. Your current saved version remains in this history.');
  ui.content.textContent='Loading versions…';
  try{
    ui.content.replaceChildren();
    const list=document.createElement('div');list.className='wiki-version-list';
    const preview=document.createElement('div');preview.className='wiki-version-preview md-preview';
    const title=document.createElement('h3'),body=document.createElement('div'),restore=document.createElement('button');
    restore.className='btn btn-accent';restore.textContent='Restore this version';ui.footer.prepend(restore);
    preview.append(title,body);ui.content.append(list,preview);ui.content.classList.add('wiki-versions');let chosen,previewGeneration=0;
    restore.disabled=true;
    const historyURL='/history?id='+encodeURIComponent(noteID)+'&scope='+scope;
    async function choose(version){
      const generation=++previewGeneration;chosen=null;restore.disabled=true;title.textContent=version.title||'(untitled)';body.textContent='Loading version…';ui.error.textContent='';
      list.querySelectorAll('.wiki-version-item').forEach(button=>button.classList.toggle('selected',button.dataset.id===String(version.id)));
      try{
        const result=await api(historyURL+'&version='+encodeURIComponent(version.id));
        if(generation!==previewGeneration||!ui.dialog.open)return;
        if(!result.version)throw Error('Version unavailable.');
        chosen=result.version;body.innerHTML=renderMarkdown(chosen.body||'');restore.disabled=false;
      }catch(error){if(generation===previewGeneration){body.textContent='';ui.error.textContent='Could not load this version: '+error.message;}}
    }
    async function loadVersions(before=''){
      const result=await api(historyURL+(before?'&before='+encodeURIComponent(before):''));
      if(!ui.dialog.open)return;
      list.querySelector('.wiki-history-more')?.remove();
      if(!before&&!(result.versions||[]).length){body.textContent='No previous versions yet.';return;}
      for(const version of result.versions||[]){
        const button=document.createElement('button');button.className='wiki-version-item';button.dataset.id=version.id;
        button.textContent=new Date(version.createdAt).toLocaleString();button.onclick=()=>choose(version);list.append(button);
      }
      if(result.nextBefore){
        const more=document.createElement('button');more.className='btn wiki-history-more';more.textContent='Older versions';
        more.onclick=async()=>{more.disabled=true;try{await loadVersions(result.nextBefore);}catch(error){ui.error.textContent='Could not load older versions: '+error.message;more.disabled=false;}};list.append(more);
      }
      if(!before)await choose(result.versions[0]);
    }
    await loadVersions();
    restore.onclick=async()=>{
      if(!chosen||selectedId!==noteID){ui.error.textContent='The active note changed. Reopen its history.';return;}
      if(!await PrismModal.confirm('Restore this version? The current saved text will remain in version history.',{okLabel:'Restore version'}))return;
      if(selectedId!==noteID)return;
      if(!await saveNote()){ui.error.textContent='Your current edits could not be saved. Nothing was restored.';return;}
      const draft=noteDraft();restore.disabled=true;ui.error.textContent='';
      try{
        const result=await api('/restore',PrismAPI.body('POST',{id:noteID,scope,version:chosen.id}));
        if(selectedId===noteID&&noteDraft()===draft){wikiRenameID(noteID,result.id);selectedId=result.id;}
        await load(true);ui.dialog.close();PrismAPI.toast('Version restored.','ok');
      }catch(error){ui.error.textContent='Not restored: '+error.message;}
      finally{restore.disabled=false;}
    };
  }catch(error){ui.content.textContent='';ui.error.textContent='Could not load history: '+error.message;}
}
async function showNotesTrash() {
  const ui=wikiDialog('Notes trash','Deleted pages remain here until you restore them.');ui.content.textContent='Loading…';
  try{
    const result=await api('/trash');ui.content.replaceChildren();
    if(!(result.notes||[]).length){ui.content.textContent='Trash is empty.';return;}
    for(const note of result.notes){
      const row=document.createElement('div');row.className='wiki-trash-row';const details=document.createElement('div');
      const title=document.createElement('strong');title.textContent=note.title||'(untitled)';
      const meta=document.createElement('small');meta.textContent=(note.scope==='group'?'Group publication':'Personal')+' · '+new Date(note.deletedAt).toLocaleString();
      details.append(title,meta);const restore=document.createElement('button');restore.className='btn';restore.textContent='Restore';
      restore.onclick=async()=>{
        restore.disabled=true;ui.error.textContent='';
        try{
          const result=await api('/restore',PrismAPI.body('POST',{id:note.id,scope:note.scope||'personal'}));
          await load();restore.textContent='Open';restore.disabled=false;
          restore.onclick=async()=>{if(await select(result.id))ui.dialog.close();};
        }catch(error){ui.error.textContent='Not restored: '+error.message;restore.disabled=false;}
      };row.append(details,restore);ui.content.append(row);
    }
  }catch(error){ui.content.textContent='';ui.error.textContent='Could not load trash: '+error.message;}
}
