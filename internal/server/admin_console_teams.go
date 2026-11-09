package server

const adminTeamsJS = `
let teamsLoadVersion=0,teamsLoadedGroup='',teamsBusy=false;
async function teamsRequest(url,options){const r=await fetch(url,options);let d;try{d=await r.json();}catch{throw new Error('Unexpected server response');}if(!r.ok)throw new Error(d.error||'Request failed ('+r.status+')');return d;}
function teamsButtons(){const ready=teamsLoadedGroup===$('ag-group').value&&!teamsBusy;for(const id of ['tm-save','tm-disconnect'])$(id).disabled=!ready;}
async function loadTeams(){const g=$('ag-group').value,v=++teamsLoadVersion;teamsLoadedGroup='';teamsButtons();
 for(const id of ['tm-app','tm-tenant','tm-secret','tm-read','tm-write','tm-tools'])$(id).value='';
 $('tm-endpoint').value='';$('tm-room').innerHTML='<option value="">— none —</option>';$('tm-package').disabled=true;$('tm-state').textContent='Loading…';$('tm-status').textContent='';
 if(!g){$('tm-state').textContent='Choose a group.';return;}
 try{const d=await teamsRequest('/api/teams/config?group='+encodeURIComponent(g));if(v!==teamsLoadVersion||g!==$('ag-group').value)return;
 const c=d.config||{};$('tm-app').value=c.appId||'';$('tm-tenant').value=c.tenantId||'';$('tm-read').value=c.ragRead||'';$('tm-write').value=c.ragWrite||'';$('tm-tools').value=c.tools||'';
 $('tm-endpoint').value=location.origin+d.endpoint;$('tm-endpoint-help').textContent=location.protocol==='https:'?'Set this endpoint in the bot configuration. Microsoft must be able to reach it over public HTTPS.':'This address is not HTTPS. Use your public HTTPS address followed by '+d.endpoint+' in the Microsoft bot configuration.';
 $('tm-state').textContent=d.configured?'Configuration saved — send a message in Teams to test the connection.':'Not configured.';$('tm-package').disabled=!d.configured;
 if(c.ownerConversation){const option=document.createElement('option');option.value=c.ownerConversation;option.textContent='Saved conversation — refresh to see its name';$('tm-room').append(option);$('tm-room').value=c.ownerConversation;}
 teamsLoadedGroup=g;teamsButtons();if(d.configured)await loadTeamsConversations();
 }catch(e){if(v===teamsLoadVersion)$('tm-state').textContent=e.message;}}
async function loadTeamsConversations(){const g=$('ag-group').value,v=teamsLoadVersion;if(!g||g!==teamsLoadedGroup)return;
 const keep=$('tm-room').value;try{const d=await teamsRequest('/api/teams/conversations?group='+encodeURIComponent(g));if(v!==teamsLoadVersion||g!==$('ag-group').value)return;
 const sel=$('tm-room');sel.innerHTML='<option value="">— none —</option>';for(const c of d.conversations||[]){const o=document.createElement('option');o.value=c.id;o.textContent=c.name;sel.append(o);}
 if(keep){if(!Array.from(sel.options).some(o=>o.value===keep)){const o=document.createElement('option');o.value=keep;o.textContent='Saved conversation';sel.append(o);}sel.value=keep;}
 }catch(e){if(v===teamsLoadVersion)$('tm-status').textContent=e.message;}}
async function saveTeams(){const g=$('ag-group').value;if(teamsBusy||teamsLoadedGroup!==g)return;teamsBusy=true;teamsButtons();const v=teamsLoadVersion;
 const b={appId:$('tm-app').value.trim(),tenantId:$('tm-tenant').value.trim(),ragRead:$('tm-read').value,ragWrite:$('tm-write').value,tools:$('tm-tools').value,ownerConversation:$('tm-room').value};if($('tm-secret').value.trim())b.clientSecret=$('tm-secret').value.trim();
 try{await teamsRequest('/api/teams/config?group='+encodeURIComponent(g),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b)});if(v===teamsLoadVersion){await loadTeams();$('tm-status').textContent='Saved ✓';}}
 catch(e){if(v===teamsLoadVersion)$('tm-status').textContent=e.message;}finally{teamsBusy=false;teamsButtons();}}
async function disconnectTeams(){const g=$('ag-group').value;if(teamsBusy||teamsLoadedGroup!==g)return;teamsBusy=true;teamsButtons();const v=teamsLoadVersion;
 try{await teamsRequest('/api/teams/config?group='+encodeURIComponent(g),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({disconnect:true})});if(v===teamsLoadVersion)await loadTeams();}
 catch(e){if(v===teamsLoadVersion)$('tm-status').textContent=e.message;}finally{teamsBusy=false;teamsButtons();}}
async function downloadTeamsPackage(){const g=$('ag-group').value;if(teamsLoadedGroup!==g)return;
 try{const r=await fetch('/api/teams/manifest?group='+encodeURIComponent(g));if(!r.ok){const d=await r.json();throw new Error(d.error||'Download failed');}const url=URL.createObjectURL(await r.blob());const a=document.createElement('a');a.href=url;a.download='prism-teams-g'+g+'.zip';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}catch(e){$('tm-status').textContent=e.message;}}
`
