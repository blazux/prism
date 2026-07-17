package server

import "net/http"

func (s *Server) handleRoomPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(roomPage))
}

const roomPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Spectrum · Room</title>
<style>
:root{color-scheme:light dark}
*{box-sizing:border-box}
body{font:15px/1.5 system-ui,sans-serif;margin:0;height:100vh;display:flex;flex-direction:column}
header{padding:.7rem 1rem;border-bottom:1px solid #8883;display:flex;gap:.6rem;align-items:center}
header b{font-size:1.05rem}
select{padding:.35rem;border:1px solid #8884;border-radius:6px;background:transparent;color:inherit;font:inherit}
.hint{color:#888;font-size:.85em;margin-left:auto}
#log{flex:1;overflow:auto;padding:1rem;display:flex;flex-direction:column;gap:.5rem}
.msg{max-width:75%;padding:.5rem .8rem;border-radius:12px;background:#8882}
.msg .who{font-size:.78em;font-weight:700;opacity:.7;margin-bottom:.1rem}
.msg.me{align-self:flex-end;background:#3b82f6;color:#fff}
.msg.agent{align-self:flex-start;background:#16a34a22;border:1px solid #16a34a55}
.typing{align-self:flex-start;color:#888;font-style:italic;font-size:.9em}
form{display:flex;gap:.5rem;padding:.7rem 1rem;border-top:1px solid #8883}
input#box{flex:1;padding:.6rem;border:1px solid #8884;border-radius:8px;background:transparent;color:inherit;font:inherit}
button{padding:.6rem 1rem;border:0;border-radius:8px;background:#3b82f6;color:#fff;font:inherit;font-weight:600;cursor:pointer}
</style></head><body>
<header>
<b>Room</b>
<select id="group"></select>
<span class="hint">mention <code id="an">@agent</code> to ask the shared agent</span>
</header>
<div id="log"></div>
<form id="f"><input id="box" placeholder="Message the room… (@agent to ask)" autocomplete="off"><button>Send</button></form>
<script>
const $=i=>document.getElementById(i);
let ws=null, me='', agentName='Assistant';
function esc(s){return (s||'').replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));}
function add(m){
 const el=document.createElement('div');
 el.className='msg'+(m.isAgent?' agent':(m.authorName===me?' me':''));
 el.innerHTML='<div class="who">'+esc(m.authorName)+'</div>'+esc(m.content);
 $('log').appendChild(el); $('log').scrollTop=$('log').scrollHeight;
}
function typing(on){
 let t=$('typing');
 if(on){ if(!t){t=document.createElement('div');t.id='typing';t.className='typing';t.textContent=agentName+' is thinking…';$('log').appendChild(t);$('log').scrollTop=$('log').scrollHeight;} }
 else if(t){ t.remove(); }
}
function connect(gid){
 if(ws){ws.onclose=null;ws.close();}
 $('log').innerHTML='';
 const proto=location.protocol==='https:'?'wss':'ws';
 ws=new WebSocket(proto+'://'+location.host+'/wsroom?group='+gid);
 ws.onmessage=e=>{
  const d=JSON.parse(e.data);
  if(d.type==='history'){(d.messages||[]).forEach(add);}
  else if(d.type==='message'){typing(false);add(d.message);}
  else if(d.type==='agent_typing'){typing(true);}
  else if(d.type==='room_config'){agentName=d.agentName||'Assistant';$('an').textContent='@'+agentName.replace(/ /g,'');}
 };
}
$('f').onsubmit=e=>{
 e.preventDefault();
 const v=$('box').value.trim(); if(!v||!ws||ws.readyState!==1)return;
 ws.send(JSON.stringify({type:'message',content:v})); $('box').value='';
};
$('group').onchange=()=>connect($('group').value);
async function init(){
 const d=await fetch('/api/my/groups').then(r=>r.json());
 me=d.displayName||'';
 const g=$('group');
 (d.groups||[]).forEach(x=>{const o=document.createElement('option');o.value=x.id||x.groupId;o.textContent=x.name||x.groupName;g.appendChild(o);});
 if(g.options.length){connect(g.value);}else{$('log').innerHTML='<p style="color:#888;padding:1rem">You are not in any group yet. Ask an admin to add you.</p>';}
}
init();
</script></body></html>`
