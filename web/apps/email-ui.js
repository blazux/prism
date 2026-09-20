// Layout helpers: all mailbox operations continue to use the existing API/tool.
function mailIcon(name) {
 const paths={mail:'M3 5h18v14H3z M3 6l9 7 9-7',menu:'M4 6h16M4 12h16M4 18h16',search:'M21 21l-5-5 M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0',compose:'M12 5H4v15h15v-8 M15 4l5 5 M10 14l1-5 7-7 5 5-7 7z',refresh:'M20 7a9 9 0 1 0 1 8 M20 2v6h-6',folder:'M3 5h7l2 3h9v12H3z',archive:'M3 3h18v5H3z M5 8v13h14V8 M10 12h4',trash:'M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7',reply:'M9 4l-6 6 6 6 M3 10h10a7 7 0 0 1 7 7',left:'M15 5l-7 7 7 7',right:'M9 5l7 7-7 7',arrow:'M4 12h16M14 6l6 6-6 6',more:'M5 12h.01M12 12h.01M19 12h.01',rules:'M4 6h16M4 12h16M4 18h16M8 3v6M16 9v6M10 15v6',settings:'M4 7h16M4 17h16M8 4v6M16 14v6',help:'M9 8a3 3 0 1 1 5 2c-2 1-2 2-2 4M12 18h.01 M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0',inbox:'M3 4h18v16H3z M3 13h5l2 3h4l2-3h5',sent:'M3 3l19 9-19 9 4-9z M7 12h15'};
 return '<svg class="mail-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="'+(paths[name]||paths.folder)+'"/></svg>';
}
function folderLabel(f){return ({inbox:'Inbox',sent:'Sent',drafts:'Drafts',archive:'Archive',trash:'Trash',junk:'Junk'})[f.role]||f.name;}
function renderFolderNav(){
 const nav=document.getElementById('folder-nav');if(!nav)return;nav.replaceChildren();
 const order={inbox:0,sent:1,drafts:2,archive:3,trash:4,junk:5};
 const sorted=mailFolders.filter(f=>f.selectable).slice().sort((a,b)=>(order[a.role]??6)-(order[b.role]??6)||a.name.localeCompare(b.name));
 for(const f of sorted){const b=document.createElement('button');b.className='nav-item'+(f.name===folder?' selected':'');b.title=f.name;b.setAttribute('aria-current',f.name===folder?'page':'false');b.innerHTML=mailIcon(f.role||'folder')+'<span>'+esc(folderLabel(f))+'</span>';b.onclick=()=>changeFolder(f.name);nav.appendChild(b);}
 updateMailHeading();
}
function updateMailHeading(){
 const title=document.getElementById('folder-title');if(!title)return;
 const f=mailFolders.find(f=>f.name===folder)||{name:folder,role:folder==='INBOX'?'inbox':''};title.textContent=folderLabel(f);
 document.getElementById('mail-count').textContent=searchQuery?'Results for “'+searchQuery+'”':messages.length+' loaded · '+messages.filter(m=>!m.seen).length+' unread';
 document.getElementById('page-label').textContent=searchQuery?'Search results':messages.length?(mailOffset+1)+'–'+(mailOffset+messages.length):'No messages';
 document.getElementById('clear-search').hidden=!searchQuery;
 document.getElementById('tab-all').classList.toggle('on',!unreadOnly);document.getElementById('tab-unread').classList.toggle('on',unreadOnly);
 updateTriageUI();
 document.getElementById('tab-all').setAttribute('aria-pressed',String(!unreadOnly));document.getElementById('tab-unread').setAttribute('aria-pressed',String(unreadOnly));
}
function setUnreadOnly(v){unreadOnly=v;renderList();}
function toggleFolders(open){const shell=document.getElementById('mail-shell');if(shell)shell.classList.toggle('folders-open',open??!shell.classList.contains('folders-open'));}
function emptyReading(){return '<div class="reading-empty">'+mailIcon('mail')+'<h2>Select a message</h2><p>Read, reply or organize it from here.</p></div>';}
function runMailSearch(){const q=document.getElementById('search').value.trim();q?search(q):clearMailSearch();}
function clearMailSearch(){document.getElementById('search').value='';loadInbox();}
function refreshMail(){searchQuery?search(searchQuery):loadInbox(true);}
document.addEventListener('click',e=>{for(const menu of document.querySelectorAll('.action-menu[open], .filter-menu[open]'))if(!menu.contains(e.target)||(menu.classList.contains('action-menu')&&e.target.closest('button')))menu.open=false;});
document.addEventListener('keydown',e=>{if(e.key==='Escape'){toggleFolders(false);for(const m of document.querySelectorAll('.action-menu[open], .filter-menu[open]'))m.open=false;}});

function updateFilterIndicator(){
 const summary=document.getElementById('filter-summary');if(!summary)return;
 const label=activeFilter.startsWith('tag:')?'#'+activeFilter.slice(4):activeFilter;
 summary.classList.toggle('has-filter',!!activeFilter);
 summary.title=label?'Active filter: '+label:'Filter by category or tag';
 summary.setAttribute('aria-label',label?'Filters, active: '+label:'Filter by category or tag');
 document.getElementById('filter-active').hidden=!activeFilter;
 document.getElementById('reset-mail-filter').disabled=!activeFilter;
}
