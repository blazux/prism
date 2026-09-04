package server

import "net/http"

// The admin console is a settings-styled page (left nav + tab panes, themed via
// /style.css) that consolidates the global-admin and group-admin controls. Tabs
// are shown by role: global admins get Users/Groups/Tools; anyone who administers
// a group gets Shared agent + Tool access for their group(s). Non-admins never
// see it (the entry link is hidden, and every endpoint is gated server-side).
func (s *Server) handleAdminConsolePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(adminConsolePage))
}

const adminConsolePage = `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Admin — PRISM</title>
<link rel="icon" type="image/svg+xml" href="/logo.svg">
<link rel="stylesheet" href="/style.css">
<script src="/theme.js"></script>
<script src="/modal.js"></script>
<style>
html,body{height:100%;overflow:hidden;margin:0}
#adm{display:flex;flex-direction:column;height:100vh}
#adm-top{display:flex;align-items:center;gap:10px;padding:10px 16px;border-bottom:1px solid var(--border)}
#adm-top b{font-size:14px}
#adm-top .sp{margin-left:auto;color:var(--text3);font-size:12px}
#adm-top a{color:var(--text3);font-size:12px;text-decoration:none;border:1px solid var(--border2);padding:4px 10px;border-radius:var(--radius)}
#adm-top a:hover{color:var(--text);border-color:var(--accent)}
#adm-main{display:flex;flex:1;min-height:0}
#adm-nav{width:200px;flex-shrink:0;border-right:1px solid var(--border);background:var(--bg1);padding:12px 0;display:flex;flex-direction:column;gap:2px}
.nav-item{display:flex;align-items:center;gap:10px;padding:9px 18px;cursor:pointer;color:var(--text3);font-size:13px;font-weight:500;transition:color .15s,background .15s;user-select:none}
.nav-item:hover{color:var(--text);background:var(--bg2)}
.nav-item.active{color:var(--text);background:var(--bg3)}
#adm-content{flex:1;overflow-y:auto;padding:20px 24px 40px;min-width:0}
.pane{display:none;max-width:760px}
.pane.active{display:block}
h2{font-size:15px;margin:0 0 4px}
.hint{color:var(--text3);font-size:12px;margin-bottom:14px}
table{width:100%;border-collapse:collapse;margin-top:8px}
th,td{text-align:left;padding:7px 6px;border-bottom:1px solid var(--border);font-size:12.5px;vertical-align:top}
th{color:var(--text3);font-weight:600}
.badge{padding:1px 7px;border-radius:99px;font-size:11px;font-weight:600}
.pending{background:#f59e0b33;color:#e5c07b}.approved{background:#4dba8733;color:#4dba87}.disabled{background:#e06c7533;color:#e06c75}
.open{background:#4dba8733;color:#4dba87}.admin_only,.restricted{background:#e06c7533;color:#e06c75}.locked{background:var(--bg3);color:var(--text3)}
input,select,textarea{background:var(--bg2);color:var(--text);border:1px solid var(--border2);border-radius:6px;padding:6px 9px;font:inherit;font-size:13px;box-sizing:border-box}
textarea{width:100%;min-height:90px;resize:vertical;line-height:1.5}
label{display:block;font-size:11px;text-transform:uppercase;letter-spacing:.04em;color:var(--text2);margin:12px 0 4px}
button{background:var(--bg2);color:var(--text);border:1px solid var(--border2);border-radius:6px;padding:5px 11px;font:inherit;font-size:12.5px;cursor:pointer}
button.primary{background:var(--accent);border-color:var(--accent);color:#fff}
button.mini{padding:3px 8px;font-size:11.5px;margin:0 3px 3px 0}
.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:8px 0}
/* Le <input type=file> natif est laid et non thématisable : on le masque et on
   pilote le clic depuis un vrai bouton, qui affiche ensuite le nom du fichier. */
.filebtn>input[type=file]{display:none}
.filebtn{display:inline-flex;align-items:center;gap:8px}
.filename{color:var(--text3);font-size:12px;max-width:180px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.coldesc{color:var(--text3);font-size:12px;margin-top:2px}
.coldesc.missing{color:var(--yellow)}
.grp{border:1px solid var(--border);border-radius:10px;padding:12px;margin:10px 0;background:var(--bg1)}
.tag{display:inline-block;background:var(--bg3);border-radius:6px;padding:2px 8px;margin:2px;font-size:12px}
.tag button{all:unset;cursor:pointer;color:var(--red);margin-left:5px;font-weight:700}
.list{max-height:340px;overflow:auto;border:1px solid var(--border);border-radius:10px;margin-top:8px}
.list>div{display:flex;justify-content:space-between;align-items:center;gap:8px;padding:7px 11px;border-bottom:1px solid var(--border);font-size:12.5px}
.tool-box{border:1px solid var(--border);border-radius:10px;margin-top:12px;overflow:hidden}
.tool-row{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 14px;border-bottom:1px solid var(--border)}
.tool-row:last-child{border-bottom:none}
.tool-row:hover{background:var(--bg1)}
.tool-row code{font-size:12.5px;color:var(--text)}
.tool-ctrl{display:flex;align-items:center;gap:10px}
.tool-ctrl .st{font-size:11px;color:var(--text3);min-width:80px;text-align:right}
.tgl{position:relative;display:inline-block;width:38px;height:21px;flex-shrink:0}
.tgl input{opacity:0;width:0;height:0;margin:0;position:absolute}
.tgl .sl{position:absolute;inset:0;background:var(--bg3);border:1px solid var(--border2);border-radius:99px;cursor:pointer;transition:background .15s,border-color .15s}
.tgl .sl:before{content:"";position:absolute;height:15px;width:15px;left:2px;top:2px;background:var(--text3);border-radius:50%;transition:transform .15s,background .15s}
.tgl input:checked + .sl{background:color-mix(in srgb,var(--accent) 60%,var(--bg3));border-color:var(--accent)}
.tgl input:checked + .sl:before{transform:translateX(17px);background:#fff}
.tgl input:disabled + .sl{opacity:.4;cursor:not-allowed}
code{font-size:12px}
#status{margin-left:8px;color:var(--accent);font-size:12px}
</style></head><body><div id="adm">
<div id="adm-top">
<a id="adm-back" href="/" target="_top" style="display:flex;align-items:center;gap:6px;text-decoration:none;color:var(--text3);font-size:12.5px;transition:color .15s" onmouseover="this.style.color='var(--text)'" onmouseout="this.style.color='var(--text3)'"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>PRISM</a>
<span style="color:var(--text3);font-size:12px">›</span>
<span style="color:var(--text2);font-size:13px;font-weight:600">Administration</span>
<span class="sp"></span><span id="who"></span></div>
<div id="adm-main">
  <div id="adm-nav"></div>
  <div id="adm-content">
    <div class="pane" data-pane="users"><h2>Users</h2><div class="hint">Approve accounts and manage roles.</div>
      <table id="utbl"><thead><tr><th>User</th><th>Status</th><th>Role</th><th>Actions</th></tr></thead><tbody></tbody></table></div>
    <div class="pane" data-pane="groups"><h2>Groups</h2><div class="hint">Create groups, manage members and per-group model access.</div>
      <div class="row"><input id="gname" placeholder="new group name"><button class="primary" onclick="createGroup()">Create</button></div>
      <div id="groups"></div></div>
    <div class="pane" data-pane="tools"><h2>Global tool policy</h2><div class="hint"><b>Open to members</b> — everyone's agent can call it. <b>Admins only</b> — members are blocked. <b>Disabled</b> — nobody, admins and shared agents included. This is the ceiling — groups can only tighten it further. Everything is open by default on this trusted deployment.</div>
      <div id="gtools" class="tool-box"></div></div>
    <div class="pane" data-pane="usage"><h2>Usage</h2><div class="hint">Activity across the platform — counts only, no conversation content. <select id="us-days" onchange="loadUsage()" style="margin-left:6px"><option value="1">last 24 h</option><option value="7" selected>last 7 days</option><option value="30">last 30 days</option></select></div>
      <div id="us-cards" class="row" style="flex-wrap:wrap;gap:10px;margin:12px 0"></div>
        <div id="us-tel" style="display:none"></div>

      <div class="row" style="align-items:flex-start;gap:22px;flex-wrap:wrap">
        <div style="flex:1;min-width:260px"><h3 style="font-size:13px;margin:10px 0 6px">Chats by user</h3><div id="us-users"></div>
          <h3 style="font-size:13px;margin:16px 0 6px">Models</h3><div id="us-models"></div></div>
        <div style="flex:1;min-width:260px"><h3 style="font-size:13px;margin:10px 0 6px">Top tools</h3><div id="us-tools"></div>
          <h3 style="font-size:13px;margin:16px 0 6px">Channels</h3><div id="us-channels"></div></div>
      </div>
      <h3 style="font-size:13px;margin:18px 0 6px">Audit trail</h3><div id="us-audit" style="font-size:12px"></div>
      <h3 style="font-size:13px;margin:18px 0 6px">Recent errors</h3><div id="us-errors" style="font-size:12px"></div></div>
    <div class="pane" data-pane="logs"><h2>Logs</h2><div class="hint">Live server log (last 5000 lines kept in memory; stderr still goes to docker logs).
      <input id="lg-filter" placeholder="filter (e.g. webex, error)" style="margin-left:6px" oninput="clearTimeout(window._lgT);window._lgT=setTimeout(loadLogs,300)">
      <label style="margin-left:10px;font-size:12px"><input type="checkbox" id="lg-auto" onchange="autoLogs()"> auto-refresh</label></div>
      <pre id="lg-out" style="background:var(--bg2);border:1px solid var(--border);border-radius:8px;padding:10px;font-size:11px;line-height:1.5;max-height:70vh;overflow:auto;white-space:pre-wrap;word-break:break-all"></pre></div>
    <div class="pane" data-pane="platform"><h2>Apps</h2><div class="hint">Toggle an app <b>OFF</b> to hide it for everyone (left rail, palette, settings). Use it to remove features that don't apply to your deployment — e.g. Email when corporate mailboxes can't connect.</div>
      <div id="papps" class="tool-box"></div>
      <h2 style="margin-top:26px">Models</h2><div class="hint">Choose which chat models users can pick. <b>None selected = all models available.</b> Group-level grants (Groups pane) can tighten this further. Global admins always see every model.</div>
      <div id="pmodels" class="tool-box"></div>
      <div class="row" style="margin-top:12px"><span id="pf-st" class="st"></span></div></div>
    <div class="pane" data-pane="telephony"><h2>Telephony</h2><div class="hint">This deployment is docked with a phone stack (Vortex). The agent also answers the phone: a known caller (number on their profile) gets their own agent, an unknown one gets the switchboard configured here.</div>

      <h2 style="margin-top:18px;font-size:15px">Voice &amp; greeting — every call</h2>
      <div class="hint">How the agent sounds, and the first thing it says when it picks up. This applies to <em>every</em> caller — known or not — so it sits above the switchboard, which only shapes what it says to strangers.</div>
      <div class="row" style="gap:12px;align-items:flex-end">
        <div style="flex:1"><label>Voice</label><select id="tel-voice" style="width:100%"><option>Loading…</option></select></div>
        <button class="mini" onclick="previewVoice()">Preview</button>
      </div>
      <div id="tel-voicehint" class="hint" style="margin-top:4px"></div>
      <label style="margin-top:10px">Greeting <span class="hint" style="margin:0">— spoken on pickup</span></label>
      <textarea id="tel-greeting" placeholder="Loading…" style="min-height:60px"></textarea>
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
      <label>Switchboard knowledge base <span class="hint" style="margin:0">— its own dedicated base; what it may tell unknown callers (hours, prices, FAQ…)</span></label>
      <div id="tel-kb" class="hint">Loading…</div>
      <div class="row" style="gap:8px;margin-top:6px;align-items:center">
        <span class="filebtn"><input type="file" id="tel-kbfile" accept=".pdf,.txt,.md,.docx,.html,.csv" onchange="showPicked('tel-kbfile','tel-kbfilename')"><button type="button" onclick="document.getElementById('tel-kbfile').click()">Choose a file…</button></span>
        <span id="tel-kbfilename" class="filename"></span>
        <button class="primary" onclick="uploadVoiceKB()">Upload document</button>
        <span id="tel-kbmsg" class="hint" style="margin:0"></span>
      </div>
      <div class="row"><button class="primary" onclick="saveTelVoice()">Save switchboard</button><span id="tel-vmsg" class="hint" style="margin:0"></span></div>

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

    <div class="pane" data-pane="agent"><h2>Shared agent</h2><div class="hint">The agent your members mention in the room. It runs with the rights you set here.</div>
      <label>Group</label><select id="ag-group"></select>
      <label>Name (members mention @Name)</label><input id="ag-name" placeholder="Assistant" style="width:100%">
      <label>Avatar</label>
      <div class="row"><span id="ag-avatar"></span><input type="file" id="ag-avfile" accept="image/png,image/jpeg,image/webp,image/gif" style="display:none" onchange="if(this.files[0])uploadAgentAvatar(this.files[0])"><button onclick="document.getElementById('ag-avfile').click()">Upload</button><button onclick="rmAgentAvatar()">Remove</button><span class="hint" style="margin:0">Shown next to the shared agent in the room.</span></div>
      <label>Model</label><select id="ag-model" style="width:100%"></select>
      <label>System prompt</label><textarea id="ag-prompt" placeholder="You are the team's assistant…"></textarea>
      <label>Max iterations per turn</label><input id="ag-maxiter" type="number" min="10" max="500" step="5" placeholder="75 (default)" style="width:160px"><span class="hint">Model calls allowed for one message (each tool use is one). 10–500, blank = default. Raise it if the shared agent stops with "iteration limit reached" on long tasks.</span>
      <label class="row" style="gap:8px;cursor:pointer"><input id="ag-thinking" type="checkbox" checked style="width:auto">Extended reasoning</label><span class="hint">Thinking mode for models that have one (Qwen3, DeepSeek-R1, gpt-oss…). Off = faster, cheaper replies. No effect on Claude models.</span>
      <label class="row" style="gap:8px;cursor:pointer"><input id="ag-lean" type="checkbox" style="width:auto">Lean prompt (frontier models)</label><span class="hint">Drops the step-by-step guardrails small local models need from the system prompt — a capable model wastes turns on them. Leave off for small Ollama models; safety rules stay on either way.</span>
      <label>Reasoning effort</label><select id="ag-effort" style="width:200px"><option value="">Server default</option><option>low</option><option>medium</option><option>high</option><option>xhigh</option></select><span class="hint">How much a thinking model reasons (when extended reasoning is on). Accepted values depend on the model — gpt-oss: low/medium/high, Qwen3.8-Flash-Next: low/medium/xhigh; an unsupported one is refused by the server, pick another.</span>
      <div class="row"><button class="primary" onclick="saveAgent()">Save agent</button><span id="status"></span></div>
      <h2 style="margin-top:26px">Webex integration</h2><div class="hint">Connect a Webex bot so members can talk to this shared agent in Webex spaces — it answers when @mentioned (group spaces) or on every message (1:1). Create a bot at developer.webex.com and paste its access token.</div>
      <label>Bot access token</label><input id="wx-token" type="password" placeholder="paste token to connect / change" style="width:100%" autocomplete="new-password">
      <div id="wx-state" style="font-size:12px;margin:6px 0;color:var(--text3)"></div>
      <div class="hint" style="margin-top:8px">Per-sender permissions — comma/space/newline-separated emails; <code>*</code> = everyone in the space.</div>
      <label>Query knowledge base</label><input id="wx-read" placeholder="*  or  a@corp.com, b@corp.com" style="width:100%">
      <label>Modify knowledge base</label><input id="wx-write" placeholder="admin@corp.com" style="width:100%">
      <label>Trigger tools (web, MCP, docker…)</label><input id="wx-tools" placeholder="admin@corp.com" style="width:100%">
      <label style="margin-top:12px">Announcement room</label>
      <div class="hint" style="margin-bottom:6px">Where scheduled jobs post (cron with deliver:"webex"). Only group spaces the bot was added to are listed — add the bot to a space, then refresh.</div>
      <div class="row"><select id="wx-room" style="min-width:260px"></select><button onclick="loadWebexRooms()">Refresh list</button><span id="wx-room-status" class="hint" style="margin:0"></span></div>
      <div class="row"><button class="primary" onclick="saveWebex()">Save Webex</button><button onclick="disconnectWebex()">Disconnect</button><span id="wx-status"></span></div></div>
    <div class="pane" data-pane="rag"><h2>Knowledge base (RAG)</h2><div class="hint">Collections shared with the whole group: the shared agent and every member's agent search them. Members see them read-only in their settings — you curate them here. Uploading to a new name creates the collection.</div>
      <label>Group</label><select id="rg-group"></select>
      <div id="grag" class="list" style="max-height:none"></div>
      <div class="row">
        <select id="grag-col" style="min-width:190px"></select>
        <button onclick="newGroupCol()">+ New collection</button>
        <span class="filebtn"><input type="file" id="grag-file" onchange="showPicked('grag-file','grag-filename')"><button type="button" onclick="document.getElementById('grag-file').click()">Choose a file…</button></span>
        <span id="grag-filename" class="filename"></span>
        <button class="primary" onclick="uploadGroupDoc()">Upload</button><span id="grag-status"></span>
      </div>
      <div id="grag-prog" style="display:none;margin:0 0 8px">
        <div style="background:var(--bg3);border-radius:4px;height:6px;overflow:hidden">
          <div id="grag-bar" style="background:var(--accent);height:100%;width:0;transition:width .3s"></div>
        </div>
        <div id="grag-prog-text" class="hint" style="margin:5px 0 0"></div>
      </div></div>
    <div class="pane" data-pane="mcp"><h2>MCP servers</h2><div class="hint">Servers connected here are shared with the whole group: the shared agent and every member's personal agent can call their tools. Members see this list read-only in their settings.</div>
      <label>Group</label><select id="mc-group"></select>
      <div id="gmcp" class="list" style="max-height:none"></div>
      <div class="row"><input id="gmcp-name" placeholder="name"><input id="gmcp-url" placeholder="http://host:port/mcp" style="flex:1;min-width:220px"><select id="gmcp-secret"><option value="">— no auth —</option></select><button class="primary" onclick="addGroupMCP()">Connect</button><span id="gmcp-status"></span></div>
      <div class="hint">Transport (Streamable HTTP / legacy SSE) is auto-detected from the URL. To authenticate, pick a stored secret — it is sent as a <code>Bearer</code> token, or create one inline from the picker below.</div></div>
    <div class="pane" data-pane="secrets"><h2>Group secrets</h2><div class="hint">Secrets stored here are scoped to this group only — used for its MCP servers / group tools, isolated from every other group. Members see the list read-only; only group admins add or remove them.</div>
      <label>Group</label><select id="gs-group"></select>
      <div id="gsecrets" class="list" style="max-height:none"></div>
      <div class="row"><input id="gs-name" placeholder="name"><input id="gs-value" type="password" placeholder="value"><button class="primary" onclick="addGroupSecret()">Save</button><span id="gs-status"></span></div></div>
    <div class="pane" data-pane="access"><h2>Group tool access</h2><div class="hint">Toggle a tool <b>ON</b> to let your group's members use it, <b>OFF</b> to restrict it to admins. You can only tighten the global policy, never loosen it — tools locked globally stay admin-only.</div>
      <label>Group</label><select id="ac-group"></select>
      <div id="ac-tools" class="tool-box"></div></div>
  </div>
</div></div>
<script>
const $=i=>document.getElementById(i);
let USERS=[],MODELS=[],ME=null,MY=null,ALLGROUPS=[];
function esc(s){return (s||'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));}
async function jget(u){const r=await fetch(u);if(!r.ok)return null;return r.json();}
async function jpost(u,b){return fetch(u,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b)});}
function show(p){document.querySelectorAll('.pane').forEach(x=>x.classList.toggle('active',x.dataset.pane===p));
 document.querySelectorAll('.nav-item').forEach(x=>x.classList.toggle('active',x.dataset.pane===p));}

// ── Users ──
async function loadUsers(){const d=await jget('/api/admin/users');if(!d)return;USERS=d.users||[];
 const tb=$('utbl').querySelector('tbody');tb.innerHTML='';
 USERS.forEach(u=>{const a=[];
  if(u.status!=='approved')a.push('<button class="mini primary" onclick="uact('+u.id+',\'approve\')">Approve</button>');
  if(u.status!=='disabled')a.push('<button class="mini" onclick="uact('+u.id+',\'disable\')">Disable</button>');
  a.push(u.role==='global_admin'?'<button class="mini" onclick="uact('+u.id+',\'make_member\')">Revoke admin</button>':'<button class="mini" onclick="uact('+u.id+',\'make_admin\')">Make admin</button>');
  const tr=document.createElement('tr');
  tr.innerHTML='<td><b>'+esc(u.displayName)+'</b><br><span style="color:var(--text3)">'+esc(u.email)+'</span></td><td><span class="badge '+u.status+'">'+u.status+'</span></td><td style="color:var(--text3)">'+(u.role==='global_admin'?'admin':'member')+'</td><td>'+a.join('')+'</td>';
  tb.appendChild(tr);});}
async function uact(id,action){
 if(action==='disable'){const u=USERS.find(x=>x.id===id);
  if(!await PrismModal.confirm('Disable '+(u?u.displayName:'this user')+'? They lose access immediately (re-approve to restore).',{danger:true,okLabel:'Disable'}))return;}
 const r=await jpost('/api/admin/users',{id,action});
 if(!r.ok){try{const d=await r.json();PrismModal.alert(d.error||'Action failed.');}catch(e){PrismModal.alert('Action failed.');}}
 loadUsers();}

// ── Groups ──
async function createGroup(){const n=$('gname').value.trim();if(!n)return;await jpost('/api/admin/groups',{action:'create',name:n});$('gname').value='';loadGroups();fillGroupPickers();}
async function gact(b){
 if(b.action==='delete'){if(!await PrismModal.confirm('Delete this group? Its shared knowledge base, MCP servers, Webex bot, room history and shared notes are removed. Members keep their personal data.',{danger:true}))return;}
 const r=await jpost('/api/admin/groups',b);
 if(!r.ok){try{const d=await r.json();PrismModal.alert(d.error||'Action failed.');}catch(e){PrismModal.alert('Action failed.');}}
 loadGroups();if(b.action==='delete')fillGroupPickers();}
async function gmodel(g,m,a){await jpost('/api/admin/group-models',{groupId:g,model:m,action:a});loadGroups();}
async function loadGroups(){const d=await jget('/api/admin/groups');if(!d)return;ALLGROUPS=d.groups||ALLGROUPS;fillGroupPickers();const box=$('groups');box.innerHTML='';
 (d.groups||[]).forEach(g=>{
  const mo=USERS.map(u=>'<option value="'+u.id+'">'+esc(u.displayName)+'</option>').join('');
  const dm=MODELS.map(m=>'<option value="'+esc(m)+'">'+esc(m)+'</option>').join('');
  const mem=(g.members||[]).map(m=>'<span class="tag">'+esc(m.displayName)+(m.role==='admin'?' (admin)':'')+' <button onclick="gact({action:\'remove_member\',groupId:'+g.id+',userId:'+m.userId+'})">×</button></span>').join('')||'<span style="color:var(--text3)">no members</span>';
  const mods=(g.models&&g.models.length)?g.models.map(m=>'<span class="tag">'+esc(m)+' <button onclick="gmodel('+g.id+',\''+esc(m)+'\',\'remove\')">×</button></span>').join(''):'<span style="color:var(--text3)">all models</span>';
  const el=document.createElement('div');el.className='grp';
  el.innerHTML='<div class="row"><b>'+esc(g.name)+'</b><button class="mini" onclick="gact({action:\'delete\',groupId:'+g.id+'})">delete</button></div>'+
   '<div class="row">Members: '+mem+'</div>'+
   '<div class="row"><select id="mu'+g.id+'">'+mo+'</select><select id="mr'+g.id+'"><option value="member">member</option><option value="admin">admin</option></select><button class="mini" onclick="gact({action:\'add_member\',groupId:'+g.id+',userId:+document.getElementById(\'mu'+g.id+'\').value,role:document.getElementById(\'mr'+g.id+'\').value})">add</button></div>'+
   '<div class="row">Models: '+mods+'</div>'+
   (MODELS.length?'<div class="row"><select id="gm'+g.id+'">'+dm+'</select><button class="mini" onclick="gmodel('+g.id+',document.getElementById(\'gm'+g.id+'\').value,\'add\')">grant model</button></div>':'');
  box.appendChild(el);});}

// ── Global tool policy ──
async function loadTools(){const d=await jget('/api/admin/tool-policy');if(!d)return;const box=$('gtools');box.innerHTML='';
 (d.tools||[]).forEach(t=>{const row=document.createElement('div');row.className='tool-row';let ctrl;
  if(t.hardFloor){ctrl='<span class="st">admin-only 🔒</span>';}
  else{const a=t.access||'open';
   ctrl='<select onchange="setPol(\''+esc(t.tool)+'\',this.value)" style="font-size:11.5px;padding:2px 6px">'+
    '<option value="open"'+(a==='open'?' selected':'')+'>Open to members</option>'+
    '<option value="admin_only"'+(a==='admin_only'?' selected':'')+'>Admins only</option>'+
    '<option value="disabled"'+(a==='disabled'?' selected':'')+'>Disabled</option></select>';}
  row.innerHTML='<code style="'+(t.access==='disabled'?'opacity:.45;text-decoration:line-through':'')+'">'+esc(t.tool)+'</code><span class="tool-ctrl">'+ctrl+'</span>';box.appendChild(row);});}
async function setPol(t,a){await jpost('/api/admin/tool-policy',{tool:t,access:a});loadTools();}

// ── Shared agent (group admin) ──
// Global admins administer every group (even ones they're not a member of);
// group admins only the groups where they hold the admin role.
function adminGroups(){return MY.isGlobalAdmin?ALLGROUPS:(MY.groups||[]).filter(g=>g.role==='admin');}
function fillGroupPickers(){const opts=adminGroups().map(g=>'<option value="'+(g.groupId||g.id)+'">'+esc(g.groupName||g.name)+'</option>').join('');
 ['ag-group','ac-group','rg-group','mc-group','gs-group'].forEach(i=>{if($(i))$(i).innerHTML=opts;});}
async function loadAgent(){const g=$('ag-group').value;if(!g)return;const c=await jget('/api/room/config?group='+g);if(!c)return;
 $('ag-name').value=c.agentName||'';$('ag-prompt').value=c.agentPrompt||'';$('ag-model').value=c.agentModel||'';
 $('ag-maxiter').value=c.agentMaxIter||'';$('ag-thinking').checked=c.agentThinking!==false;$('ag-lean').checked=c.agentLean===true;$('ag-effort').value=c.agentReasoning||'';renderAgentAvatar();}
// ── Shared-agent avatar ──
function avInitials(n){return (n||'?').trim().split(/\s+/).map(w=>w[0]||'').slice(0,2).join('').toUpperCase()||'?';}
function avBox(scope,name,ver){const px=44,fs=18,src='/api/avatar?scope='+encodeURIComponent(scope)+(ver?'&v='+ver:'');
 return '<span style="position:relative;display:inline-flex;width:'+px+'px;height:'+px+'px;border-radius:50%;overflow:hidden;background:#6b8afd;color:#fff;align-items:center;justify-content:center;font-weight:600;font-size:'+fs+'px">'+esc(avInitials(name))+'<img src="'+src+'" style="position:absolute;inset:0;width:100%;height:100%;object-fit:cover" onerror="this.remove()"></span>';}
function renderAgentAvatar(ver){const g=$('ag-group').value;if(!g)return;$('ag-avatar').innerHTML=avBox('agent-g'+g,$('ag-name').value||'Agent',ver);}
async function downscale(file){const img=await createImageBitmap(file);const s=Math.min(1,256/Math.max(img.width,img.height));const w=Math.max(1,Math.round(img.width*s)),h=Math.max(1,Math.round(img.height*s));const c=document.createElement('canvas');c.width=w;c.height=h;c.getContext('2d').drawImage(img,0,0,w,h);return await new Promise(r=>c.toBlob(r,'image/png'));}
async function uploadAgentAvatar(file){const g=$('ag-group').value;if(!g)return;const blob=await downscale(file);const fd=new FormData();fd.append('file',blob,'a.png');const r=await fetch('/api/avatar?scope=agent-g'+g,{method:'POST',body:fd});if(r.ok){renderAgentAvatar(Date.now());}else{$('status').textContent='avatar error';}}
async function rmAgentAvatar(){const g=$('ag-group').value;if(!g)return;await fetch('/api/avatar?scope=agent-g'+g,{method:'DELETE'});renderAgentAvatar(Date.now());}
async function saveAgent(){const g=$('ag-group').value;const r=await jpost('/api/room/config?group='+g,{agentName:$('ag-name').value,agentPrompt:$('ag-prompt').value,agentModel:$('ag-model').value,
 agentMaxIter:parseInt($('ag-maxiter').value,10)||0,agentThinking:$('ag-thinking').checked,agentLean:$('ag-lean').checked,agentReasoning:$('ag-effort').value});
 $('status').textContent=r.ok?'saved ✓':'error';setTimeout(()=>$('status').textContent='',2000);}
// ── Webex (per-group bot for the shared agent) ──
// Les salles viennent de l'API Webex (GET /v1/rooms via le token du bot) : on ne
// peut donc les lister qu'une fois le bot connecté. On garde la salle enregistrée
// même si la liste échoue, sinon un simple souci réseau la ferait disparaître au
// prochain « Save ».
async function loadWebexRooms(){const g=$('ag-group').value;if(!g)return;
 const st=$('wx-room-status');const sel=$('wx-room');const keep=sel.value||window._wxRoom||'';
 st.textContent='loading…';
 const d=await jget('/api/webex/rooms?group='+g);
 if(!d||!d.rooms){st.textContent='connect the bot first';return;}
 if(!d.rooms.length){sel.innerHTML='<option value="">no group space — add the bot to one</option>';st.textContent='';return;}
 sel.innerHTML='<option value="">— none —</option>'+d.rooms.map(r=>'<option value="'+esc(r.id)+'">'+esc(r.title)+'</option>').join('');
 if(keep&&d.rooms.some(r=>r.id===keep))sel.value=keep;
 st.textContent=d.rooms.length+' space'+(d.rooms.length>1?'s':'');}

async function loadWebex(){const g=$('ag-group').value;if(!g)return;const d=await jget('/api/webex/config?group='+g);if(!d)return;
 $('wx-read').value=d.ragRead||'';$('wx-write').value=d.ragWrite||'';$('wx-tools').value=d.tools||'';$('wx-token').value='';
 window._wxRoom=d.ownerRoom||'';
 const sel=$('wx-room');
 sel.innerHTML=window._wxRoom?'<option value="'+esc(window._wxRoom)+'">(saved room — refresh to see its name)</option>':'<option value="">— none —</option>';
 if(d.configured)loadWebexRooms();
 $('wx-state').textContent=d.configured?'● Connected — a bot token is set for this group.':'○ Not connected — paste a bot token to connect.';}
async function saveWebex(){const g=$('ag-group').value;if(!g)return;const b={ragRead:$('wx-read').value,ragWrite:$('wx-write').value,tools:$('wx-tools').value,ownerRoom:$('wx-room').value};
 const t=$('wx-token').value.trim();if(t)b.token=t;const r=await jpost('/api/webex/config?group='+g,b);
 $('wx-status').textContent=r.ok?'saved ✓':'error';setTimeout(()=>$('wx-status').textContent='',2000);loadWebex();}
async function disconnectWebex(){const g=$('ag-group').value;if(!g)return;await jpost('/api/webex/config?group='+g,{disconnect:true});loadWebex();}
// ── Group secrets (scoped to this group only — used by its MCP servers/tools) ──
async function loadGroupSecrets(){const g=$('gs-group').value;if(!g)return;
 const d=await jget('/api/group/secrets?group='+g);const box=$('gsecrets');box.innerHTML='';
 const names=(d&&d.secrets)||[];
 if(!names.length){box.innerHTML='<div style="color:var(--text3)">No secrets for this group.</div>';return;}
 names.forEach(n=>{const el=document.createElement('div');
  el.innerHTML='<span><b>'+esc(n)+'</b></span><button class="mini" onclick="removeGroupSecret(\''+esc(n)+'\')">remove</button>';
  box.appendChild(el);});}
async function addGroupSecret(){const g=$('gs-group').value;if(!g)return;const n=$('gs-name').value.trim(),v=$('gs-value').value;if(!n||!v)return;
 $('gs-status').textContent='saving…';
 const r=await jpost('/api/group/secrets?group='+g,{name:n,value:v});
 $('gs-status').textContent=r.ok?'saved ✓':'error';setTimeout(()=>$('gs-status').textContent='',2000);
 if(r.ok){$('gs-name').value='';$('gs-value').value='';}loadGroupSecrets();}
async function removeGroupSecret(name){const g=$('gs-group').value;if(!g)return;
 await fetch('/api/group/secrets?group='+g+'&name='+encodeURIComponent(name),{method:'DELETE'});loadGroupSecrets();}

// ── Group MCP servers (shared with the whole group) ──
async function loadGroupMCP(){const g=$('mc-group').value;if(!g)return;
 // Fill the auth-secret picker from this group's own scoped secrets (+ inline creation).
 try{const s2=await jget('/api/group/secrets?group='+g);const names=(s2&&s2.secrets)||[];
  $('gmcp-secret').innerHTML='<option value="">— no auth —</option>'+names.map(n=>'<option value="'+esc(n)+'">🔑 '+esc(n)+'</option>').join('')+'<option value="__new__">＋ New secret…</option>';
  $('gmcp-secret').onchange=async function(){if(this.value!=='__new__')return;this.value='';
   const res=await PrismModal.prompt('New group secret',[
    {name:'name',label:'Name',placeholder:'e.g. CONTEXT7_API_KEY'},
    {name:'value',label:'Value',type:'password',placeholder:'stored encrypted, scoped to this group only'}]);
   if(!res||!res.name.trim()||!res.value)return;
   const n=res.name.trim();
   const r=await jpost('/api/group/secrets?group='+g,{name:n,value:res.value});
   if(r&&r.ok){const o=document.createElement('option');o.value=n;o.textContent='🔑 '+n;
    this.insertBefore(o,this.querySelector('option[value="__new__"]'));this.value=n;}
   else{PrismModal.alert('Could not save the secret.');}};}catch(e){}
 const d=await jget('/api/group/mcp?group='+g);const box=$('gmcp');box.innerHTML='';
 const list=(d&&d.servers)||[];const pol=(d&&d.policy)||{};
 if(!list.length){box.innerHTML='<div style="color:var(--text3)">No MCP servers connected for this group.</div>';return;}
 const accessSel=t=>{const a=pol[t]||'open';
  return '<select onchange="setToolAccess(\''+esc(t)+'\',this.value)" style="font-size:11.5px;padding:2px 6px">'+
   '<option value="open"'+(a==='open'?' selected':'')+'>Open to members</option>'+
   '<option value="admin_only"'+(a==='admin_only'?' selected':'')+'>Admins only</option>'+
   '<option value="disabled"'+(a==='disabled'?' selected':'')+'>Disabled</option></select>';};
 list.forEach(sv=>{const el=document.createElement('div');el.style.flexDirection='column';el.style.alignItems='stretch';
  const auth=sv.authSecret?' · 🔑 '+esc(sv.authSecret):'';
  let html='<div style="display:flex;justify-content:space-between;align-items:center;gap:8px"><span><b>'+esc(sv.name)+'</b> <span style="color:var(--text3)">'+esc(sv.url)+' · '+((sv.tools||[]).length)+' tools'+auth+'</span></span>'+
   '<button class="mini" onclick="removeGroupMCP(\''+esc(sv.id)+'\')">remove</button></div>';
  (sv.tools||[]).forEach(t=>{const a=pol[t.name]||'open';
   html+='<div style="display:flex;justify-content:space-between;align-items:center;gap:8px;padding:3px 0 3px 14px;font-size:12px">'+
    '<code style="'+(a==='disabled'?'opacity:.45;text-decoration:line-through':'')+'">'+esc(t.name)+'</code>'+accessSel(t.name)+'</div>';});
  box.appendChild(el);el.innerHTML=html;});}
async function setToolAccess(tool,access){const g=$('mc-group').value;if(!g)return;
 await jpost('/api/group/tool-policy?group='+g,{tool:tool,access:access});loadGroupMCP();}
async function addGroupMCP(){const g=$('mc-group').value;if(!g)return;const n=$('gmcp-name').value.trim(),u=$('gmcp-url').value.trim();if(!n||!u)return;
 $('gmcp-status').textContent='connecting…';
 const r=await jpost('/api/group/mcp?group='+g,{name:n,url:u,authSecret:$('gmcp-secret').value});
 let d=null;try{d=await r.json();}catch(e){}
 if(r.ok&&d&&d.needs_oauth){
  // The server wants OAuth: open the consent popup and wait for the callback
  // page to signal completion (it posts 'mcp-oauth-done' to this window) —
  // mirrors the personal MCP flow in settings.html.
  $('gmcp-status').textContent='authorize in the popup…';
  const popup=window.open(d.authorize_url,'mcp-oauth','width=520,height=700');
  if(!popup){$('gmcp-status').textContent='popup blocked';return;}
  await new Promise(resolve=>{
   function onMsg(e){if(e.data==='mcp-oauth-done'){cleanup();resolve();}}
   const poll=setInterval(()=>{if(popup.closed){cleanup();resolve();}},700);
   function cleanup(){window.removeEventListener('message',onMsg);clearInterval(poll);}
   window.addEventListener('message',onMsg);
  });
  $('gmcp-status').textContent='connected ✓';setTimeout(()=>$('gmcp-status').textContent='',2500);
  $('gmcp-name').value='';$('gmcp-url').value='';$('gmcp-secret').value='';loadGroupMCP();
  return;
 }
 $('gmcp-status').textContent=r.ok?'connected ✓':'error';setTimeout(()=>$('gmcp-status').textContent='',2500);
 if(r.ok){$('gmcp-name').value='';$('gmcp-url').value='';$('gmcp-secret').value='';}loadGroupMCP();}
async function removeGroupMCP(id){const g=$('mc-group').value;if(!g)return;await fetch('/api/group/mcp?group='+g+'&id='+encodeURIComponent(id),{method:'DELETE'});loadGroupMCP();}
// ── Group knowledge base (RAG) ──
// La description est ce que l'agent lit pour savoir QUAND fouiller une collection :
// elle est injectée telle quelle dans son prompt système (bloc « ## Knowledge Base
// (RAG) », cf. internal/server/ws.go). Sans elle il ne voit qu'un nom et un nombre
// de documents — d'où l'avertissement en jaune.
async function loadGroupRAG(){const g=$('rg-group').value;if(!g)return;const d=await jget('/api/rag/collections?group='+g);const box=$('grag');box.innerHTML='';
 const list=Array.isArray(d)?d:[];
 const sel=$('grag-col');const keep=sel.value;
 sel.innerHTML=list.map(c=>'<option value=\"'+esc(c.name)+'\">'+esc(c.name)+'</option>').join('')||'<option value=\"\">no collection yet</option>';
 if(keep&&list.some(c=>c.name===keep))sel.value=keep;
 if(!list.length){box.innerHTML='<div style=\"color:var(--text3)\">No collections yet — create one, then upload a document.</div>';return;}
 list.forEach(c=>{const el=document.createElement('div');
  const desc=c.description||'';
  el.innerHTML='<span><b>'+esc(c.name)+'</b> <span style=\"color:var(--text3)\">'+(c.doc_count||0)+' docs</span>'+
   '<div class=\"coldesc'+(desc?'':' missing')+'\">'+(desc?esc(desc):'No description — the agent will not know when to search this collection.')+'</div></span>'+
   '<span><button class=\"mini\" onclick=\"editGroupColDesc(\''+esc(c.name)+'\')\">description</button> '+
   '<button class=\"mini\" onclick=\"deleteGroupCol(\''+esc(c.name)+'\')\">delete</button></span>';
  box.appendChild(el);});}

// Écrit nom + description via PATCH ; la collection survit même sans document.
async function saveGroupCol(g,name,description){
 const r=await fetch('/api/rag/collections?group='+g,{method:'PATCH',headers:{'Content-Type':'application/json'},
  body:JSON.stringify({name:name,description:description})});
 return r.ok;}

async function newGroupCol(){const g=$('rg-group').value;if(!g)return;
 const res=await PrismModal.prompt('New collection',[
  {name:'name',label:'Name',placeholder:'e.g. api-docs'},
  {name:'description',label:'Description — tells the agent when to search it',type:'textarea',
   placeholder:'e.g. Internal API documentation: endpoints, authentication, error codes.'}]);
 if(!res)return;
 const name=res.name.trim().toLowerCase().replace(/[^a-z0-9\-_]/g,'-').replace(/-+/g,'-');
 if(!name)return;
 if(!await saveGroupCol(g,name,res.description.trim())){$('grag-status').textContent='error';return;}
 await loadGroupRAG();$('grag-col').value=name;}

async function editGroupColDesc(name){const g=$('rg-group').value;if(!g)return;
 const cur=(await jget('/api/rag/collections?group='+g)||[]).find(c=>c.name===name);
 const res=await PrismModal.prompt('Description of "'+name+'"',[
  {name:'description',label:'Tells the agent when to search this collection',type:'textarea',
   value:(cur&&cur.description)||'',placeholder:'e.g. Internal API documentation: endpoints, authentication…'}]);
 if(!res)return;
 if(!await saveGroupCol(g,name,res.description.trim())){$('grag-status').textContent='error';return;}
 loadGroupRAG();}

// Native file inputs are unstyleable, so every upload in this console hides the input
// (.filebtn) behind a real button and echoes the chosen name into a .filename span.
// Pass both ids so the same helper serves the RAG, switchboard and voice-clone pickers.
function showPicked(input,label){const f=$(input).files[0];$(label).textContent=f?f.name:'';}
async function deleteGroupCol(name){const g=$('rg-group').value;if(!g)return;
 if(!await PrismModal.confirm('Delete collection "'+name+'" and all its documents?',{danger:true}))return;
 await fetch('/api/rag/collections?group='+g+'&name='+encodeURIComponent(name),{method:'DELETE'});loadGroupRAG();}
// L'upload est une seule requête POST synchrone : le serveur parse, embedde et
// écrit avant de répondre, ce qui prend des minutes sur un gros manuel. Pendant
// que ce POST est en vol, on interroge /api/rag/upload/progress, que le handler
// alimente à chaque tranche d'embedding.
function ragProgUI(show,pct,text){
 $('grag-prog').style.display=show?'block':'none';
 if(pct!=null)$('grag-bar').style.width=Math.max(2,Math.min(100,pct))+'%';
 if(text!=null)$('grag-prog-text').textContent=text;}

function fmtETA(s){if(!s||s<1)return '';if(s<60)return '~'+s+'s left';
 const m=Math.floor(s/60);return '~'+m+'m'+(s%60?' '+(s%60)+'s':'')+' left';}

async function pollIngest(g,col,name,stop){
 while(!stop.done){
  await new Promise(r=>setTimeout(r,700));
  if(stop.done)break;
  let d;try{d=await jget('/api/rag/upload/progress?group='+g+'&collection='+encodeURIComponent(col)+'&filename='+encodeURIComponent(name));}catch(_){continue;}
  if(!d||!d.found)continue;
  if(d.stage==='parsing'){ragProgUI(true,3,'Parsing '+name+'…');}
  else if(d.stage==='embedding'){
   // Avant la première tranche il n'y a ni débit ni ETA : ne pas afficher « 0/s ».
   if(!d.done){ragProgUI(true,3,'Embedding '+d.total+' chunks…');}
   else{const pct=Math.round(d.done/d.total*100);
    ragProgUI(true,pct,'Embedding '+d.done+'/'+d.total+' chunks ('+Math.round(d.rate)+'/s) '+fmtETA(d.etaSecs));}}
  else if(d.stage==='storing'){ragProgUI(true,99,'Storing '+d.total+' chunks…');}
  else if(d.stage==='failed'){ragProgUI(true,100,'Failed: '+(d.error||'unknown'));return;}
  else if(d.stage==='done'){ragProgUI(true,100,'Indexed '+d.total+' chunks ✓');return;}
 }}

async function uploadGroupDoc(){const g=$('rg-group').value;const col=$('grag-col').value.trim();const f=$('grag-file').files[0];
 if(!g||!col||!f){$('grag-status').textContent='collection + file required';setTimeout(()=>$('grag-status').textContent='',2500);return;}
 $('grag-status').textContent='uploading…';
 ragProgUI(true,2,'Uploading '+f.name+'…');
 const stop={done:false};
 pollIngest(g,col,f.name,stop);
 const fd=new FormData();fd.append('collection',col);fd.append('file',f);
 let r;
 try{r=await fetch('/api/rag/upload?group='+g,{method:'POST',body:fd});}
 finally{stop.done=true;}
 if(r&&r.ok){ragProgUI(true,100,'Indexed ✓');$('grag-status').textContent='ingested ✓';$('grag-file').value='';showPicked();}
 else{let msg='error';try{msg=(await r.json()).error||msg;}catch(_){}
  ragProgUI(true,100,'Failed: '+msg);$('grag-status').textContent='error';}
 setTimeout(()=>{$('grag-status').textContent='';ragProgUI(false);},4000);
 loadGroupRAG();}
async function loadAccess(){const g=$('ac-group').value;if(!g)return;const d=await jget('/api/group/tool-policy?group='+g);if(!d)return;const box=$('ac-tools');box.innerHTML='';
 (d.tools||[]).forEach(t=>{const row=document.createElement('div');row.className='tool-row';
  const locked=t.hardFloor||t.globalAdminOnly;
  const open=!locked&&!t.groupRestricted;
  let ctrl;
  if(locked){ctrl='<span class="st">admin-only</span><label class="tgl" title="Locked by the global policy"><input type="checkbox" disabled><span class="sl"></span></label>';}
  else{ctrl='<span class="st">'+(open?'All members':'Admins only')+'</span><label class="tgl"><input type="checkbox" '+(open?'checked':'')+' onchange="setAccess(\''+esc(t.tool)+'\',!this.checked)"><span class="sl"></span></label>';}
  row.innerHTML='<code>'+esc(t.tool)+'</code><span class="tool-ctrl">'+ctrl+'</span>';box.appendChild(row);});}
async function setAccess(t,restrict){await jpost('/api/group/tool-policy?group='+$('ac-group').value,{tool:t,restrict});loadAccess();}

// ── Usage & Logs (global admin) ──
function usBars(el,rows,names){const host=$(el);host.innerHTML='';if(!rows||!rows.length){host.innerHTML='<div class="hint">No data.</div>';return;}
 const max=Math.max(...rows.map(r=>r.n));
 rows.forEach(r=>{const label=names&&names[r.key]?names[r.key]:(r.key||'—');
  const row=document.createElement('div');row.style.cssText='display:flex;align-items:center;gap:8px;margin:3px 0;font-size:12px';
  row.innerHTML='<span style="width:130px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="'+esc(label)+'">'+esc(label)+'</span>'+
   '<span style="flex:1;background:var(--bg2);border-radius:4px;overflow:hidden;height:14px"><span style="display:block;height:100%;width:'+Math.max(3,Math.round(100*r.n/max))+'%;background:#6b8afd"></span></span>'+
   '<span style="width:70px;text-align:right;color:var(--text3)">'+r.n+(r.qty>r.n?' · ~'+(r.qty>=1000?Math.round(r.qty/1000)+'k':r.qty)+' tok':'')+'</span>';
  host.appendChild(row);});}
function usCard(label,val){return '<div style="background:var(--bg2);border:1px solid var(--border);border-radius:9px;padding:10px 16px;min-width:110px"><div style="font-size:21px;font-weight:700">'+val+'</div><div style="font-size:11px;color:var(--text3)">'+label+'</div></div>';}
async function loadUsage(){const days=$('us-days').value;const d=await jget('/api/admin/usage?days='+days);if(!d)return;
 const k=d.kinds||{},kd=d.kindsDay||{};
 $('us-cards').innerHTML=usCard('active today',d.activeDay||0)+usCard('active 7d',d.activeWeek||0)+
  usCard('chats today',kd.chat_turn||0)+usCard('chats '+days+'d',k.chat_turn||0)+
  usCard('tool calls '+days+'d',k.tool_call||0)+usCard('channel msgs '+days+'d',k.channel_msg||0)+usCard('logins '+days+'d',k.login||0);
 usBars('us-users',d.byUserChat,d.userNames);usBars('us-models',d.models);usBars('us-tools',d.tools);usBars('us-channels',d.channels);
 const fmt=e=>{const t=new Date(e.ts).toLocaleString();const who=d.userNames&&d.userNames[String(e.userId)]||('u'+e.userId);
  let m='';try{const o=JSON.parse(e.meta||'{}');m=Object.entries(o).filter(([k2,v])=>v!==null&&v!=='').map(([k2,v])=>k2+'='+(typeof v==='object'?JSON.stringify(v):v)).join(' ');}catch(_){ }
  return '<div style="padding:3px 0;border-bottom:1px solid var(--border)"><span style="color:var(--text3)">'+t+'</span> <b>'+esc(who)+'</b> '+esc(e.item)+' <span style="color:var(--text3)">'+esc(m.slice(0,160))+'</span></div>';};
 $('us-audit').innerHTML=(d.audit&&d.audit.length)?d.audit.map(fmt).join(''):'<div class="hint">No audit events yet.</div>';
 $('us-errors').innerHTML=(d.errors&&d.errors.length)?d.errors.map(fmt).join(''):'<div class="hint">No errors recorded. 🎉</div>';
   loadTelephonyUsage();}
// Vortex: telephony activity (from the docked Vox stack) shown alongside the rest of usage.
async function loadTelephonyUsage(){
 const box=$('us-tel');if(!box)return;
 const s=await jget('/api/vox/runtime-status');if(!s){box.style.display='none';return;}
 box.style.display='';
 const dur=x=>x==null?'—':(x<60?x+'s':Math.floor(x/60)+'m'+(x%60)+'s');
 const when=t=>{if(!t)return'—';const d=new Date(t);return d.toLocaleDateString('en-GB',{day:'2-digit',month:'2-digit'})+' '+d.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'});};
 const REASONS={hung_up:'Hung up',agent_hung_up:'Agent',transferred:'Transferred',no_answer:'No answer',failed:'Failed'};
 let html='<h3 style="font-size:13px;margin:18px 0 6px">Telephony</h3>'+
  '<div class="row" style="flex-wrap:wrap;gap:10px;margin-bottom:10px">'+
  usCard('SIP',s.sip_ok?'online':'offline')+usCard('calls today',s.calls_today||0)+usCard('total',s.total_calls||0)+
  usCard('avg duration',dur(s.avg_duration_secs))+usCard('% transferred',(s.transfer_pct||0)+'%')+usCard('active',s.active_calls||0)+'</div>';
 const cl=await jget('/api/vox/call-logs?limit=25');const items=(cl&&cl.items)||[];
 html+='<div style="font-size:12px">';
 if(!items.length)html+='<div class="hint">No calls recorded yet.</div>';
 else{html+='<table style="width:100%;border-collapse:collapse"><thead><tr style="color:var(--text3);text-align:left">'+
   '<th style="padding:5px 8px">When</th><th style="padding:5px 8px">Caller</th><th style="padding:5px 8px">Duration</th><th style="padding:5px 8px">Ended</th><th style="padding:5px 8px">Summary</th></tr></thead><tbody>';
  html+=items.map((c,i)=>{const det=(c.turns||[]).map(t=>'<div><b style="color:var(--text3)">'+(t.role==='caller'?'Caller':'Agent')+':</b> '+esc(t.text)+'</div>').join('');
   return '<tr style="cursor:pointer;border-top:1px solid var(--border)" onclick="var e=document.getElementById(\'ut'+i+'\');e.style.display=e.style.display?\'\':\'none\'">'+
    '<td style="padding:6px 8px">'+when(c.started_at)+'</td><td style="padding:6px 8px">'+esc(c.caller_number)+'</td>'+
    '<td style="padding:6px 8px">'+dur(c.duration_secs)+'</td><td style="padding:6px 8px">'+esc(REASONS[c.end_reason]||c.end_reason||'—')+'</td>'+
    '<td style="padding:6px 8px">'+esc(c.summary||'—')+'</td></tr>'+
    '<tr id="ut'+i+'" style="display:none"><td colspan="5" style="padding:6px 12px;color:var(--text2);background:var(--bg2)">'+(c.message?'<div style="margin-bottom:6px"><b>Message:</b> '+esc(c.message)+'</div>':'')+(det||'<span class="hint">No transcript.</span>')+'</td></tr>';
  }).join('');
  html+='</tbody></table>';}
 html+='</div>';
 box.innerHTML=html;
}
async function loadLogs(){const f=$('lg-filter').value.trim();const d=await jget('/api/admin/logs?limit=500&filter='+encodeURIComponent(f));if(!d)return;
 const out=$('lg-out');const atEnd=out.scrollHeight-out.scrollTop-out.clientHeight<40;
 out.textContent=(d.lines||[]).join('\n')||'(empty)';if(atEnd)out.scrollTop=out.scrollHeight;}
function autoLogs(){clearInterval(window._lgI);if($('lg-auto').checked)window._lgI=setInterval(loadLogs,4000);}

// ── Telephony (Vortex): switchboard persona (Cortex /api/voice) + SIP trunk (proxied Vox /api/vox/sip) ──
const SIP_FIELDS=['registrar','registrar_ip','username','domain','tls_port','callerid_name','transfer_method'];
// The switchboard reads a dedicated, reserved RAG scope ("voice"); documents are
// managed right here, so it's independent from any group.
async function loadVoiceKB(){
 const box=$('tel-kb');const d=await jget('/api/rag/collections?scope=voice');const list=Array.isArray(d)?d:[];
 if(!list.length){box.innerHTML='<span class="hint">Empty — the switchboard has no information to give unknown callers. Upload documents (hours, prices, services, FAQ…).</span>';return;}
 box.innerHTML=list.map(c=>'<div style="padding:3px 0;display:flex;justify-content:space-between;align-items:center"><span><b>'+esc(c.name)+'</b> <span style="color:var(--text3)">'+(c.doc_count||0)+' docs</span></span><button class="mini" onclick="deleteVoiceKB(\''+esc(c.name)+'\')">delete</button></div>').join('');
}
async function uploadVoiceKB(){
 const f=$('tel-kbfile').files[0];const m=$('tel-kbmsg');
 if(!f){m.textContent='Pick a file first.';return;}
 m.textContent='Uploading & indexing…';
 // Ensure the collection carries a description so the agent knows when to search it.
 await fetch('/api/rag/collections?scope=voice',{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:'switchboard',description:'Public information for phone callers: opening hours, prices, services offered, and frequently asked questions.'})});
 const fd=new FormData();fd.append('collection','switchboard');fd.append('file',f);
 let r;try{r=await fetch('/api/rag/upload?scope=voice',{method:'POST',body:fd});}catch(e){m.textContent='Failed: '+e.message;return;}
 m.textContent=r.ok?'✓ Added':'Failed ('+r.status+')';
 $('tel-kbfile').value='';$('tel-kbfilename').textContent='';
 loadVoiceKB();setTimeout(()=>m.textContent='',3500);
}
async function deleteVoiceKB(name){
 if(!await PrismModal.confirm('Delete "'+name+'" and all its documents?',{danger:true}))return;
 await fetch('/api/rag/collections?scope=voice&name='+encodeURIComponent(name),{method:'DELETE'});loadVoiceKB();
}
async function loadTelephony(){
 const v=await jget('/api/voice');
 if(v)$('tel-persona').value=v.personality||'';
 loadVoiceKB();
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
 const rc=await fetch('/api/vox/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({values:{greeting:$('tel-greeting').value}})});
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
}
async function saveCfgFields(spec,msgId){
 const m=$(msgId);m.textContent='Saving…';
 const values={};spec.forEach(([k])=>{const el=$('cf-'+k);if(el)values[k]=el.value;});
 const r=await fetch('/api/vox/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({values})});
 m.textContent=r.ok?'✓ Saved':'Failed ('+r.status+')';
 if(r.ok)Object.assign(TEL_CFG,values);
 setTimeout(()=>m.textContent='',2500);
}
const savePhrases =()=>saveCfgFields(TEL_PHRASES,'tel-pmsg');
const saveDict    =()=>saveCfgFields(TEL_DICT,'tel-dmsg');
const saveHandling=()=>saveCfgFields(TEL_HANDLING,'tel-hmsg');

// Outbound calls are deliberately NOT an admin form here. In Vortex you place a call
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

// ── Platform (global admin): apps on/off + model allow-list ──
let PF={apps:[],disabledApps:[],allModels:[],allowedModels:[]};
const APP_LABELS={email:'Email',notes:'Notes',tasks:'Tasks',calendar:'Calendar',room:'Room (group chat)'};
async function loadPlatform(){const d=await jget('/api/admin/platform');if(!d)return;PF=d;
 const ab=$('papps');ab.innerHTML='';(PF.apps||[]).forEach(a=>{const off=PF.disabledApps.includes(a);
  const row=document.createElement('div');row.className='tool-row';
  row.innerHTML='<code>'+esc(APP_LABELS[a]||a)+'</code><span class="tool-ctrl"><span class="st">'+(off?'Hidden':'Enabled')+'</span><label class="tgl"><input type="checkbox" '+(off?'':'checked')+' onchange="setApp(\''+esc(a)+'\',this.checked)"><span class="sl"></span></label></span>';
  ab.appendChild(row);});
 const mb=$('pmodels');mb.innerHTML='';
 if(!(PF.allModels||[]).length){mb.innerHTML='<div class="hint">No models reported by the backend.</div>';return;}
 PF.allModels.forEach(m=>{const on=PF.allowedModels.includes(m);
  const row=document.createElement('div');row.className='tool-row';
  row.innerHTML='<code>'+esc(m)+'</code><span class="tool-ctrl"><span class="st">'+(PF.allowedModels.length?(on?'Allowed':'Hidden'):'All allowed')+'</span><label class="tgl"><input type="checkbox" '+(on?'checked':'')+' onchange="setModel(\''+esc(m)+'\',this.checked)"><span class="sl"></span></label></span>';
  mb.appendChild(row);});}
async function setApp(a,enabled){const d=new Set(PF.disabledApps);enabled?d.delete(a):d.add(a);
 const r=await jpost('/api/admin/platform',{disabledApps:[...d]});$('pf-st').textContent=r&&r.ok?'saved ✓':'error';setTimeout(()=>$('pf-st').textContent='',1500);loadPlatform();}
async function setModel(m,allowed){const s2=new Set(PF.allowedModels);allowed?s2.add(m):s2.delete(m);
 const r=await jpost('/api/admin/platform',{allowedModels:[...s2]});$('pf-st').textContent=r&&r.ok?'saved ✓':'error';setTimeout(()=>$('pf-st').textContent='',1500);loadPlatform();}

async function init(){
 ME=await jget('/api/me'); MY=await jget('/api/my/groups')||{groups:[]};
 if(!ME||!ME.authenticated){location.href='/login';return;}
 // Single-user (legacy) /api/me has no user object: fall through to the
 // "no admin access" message instead of crashing on ME.user.role.
 const isGA=ME.user?.role==='global_admin'; MY.isGlobalAdmin=isGA;
 $('who').textContent=(ME.user?.displayName||'')+(ME.user?' · ':'')+(isGA?'global admin':ME.user?'group admin':'single-user mode — no admin console');
 try{const m=await fetch('/api/models').then(r=>r.json());MODELS=m.models||[];}catch(e){}
 // Global admin: load every group so the shared-agent / tool-access tabs and
 // group pickers cover all of them, not just the admin's own memberships.
 if(isGA){try{const g=await jget('/api/admin/groups');ALLGROUPS=(g&&g.groups)||[];}catch(e){}}
 const nav=$('adm-nav');const items=[];
 if(isGA){items.push(['users','Users'],['groups','Groups'],['tools','Tools'],['platform','Platform'],['usage','Usage'],['logs','Logs']);}
 // Vortex: telephony admin (switchboard persona + SIP trunk) — only when docked with Vox.
 let VORTEX=false;try{VORTEX=!!(await fetch('/api/platform').then(r=>r.json())).vortexMode;}catch(e){}
 if(isGA&&VORTEX){items.push(['telephony','Telephony']);}
 if(adminGroups().length){items.push(['agent','Shared agent'],['rag','RAG'],['mcp','MCP'],['secrets','Secrets'],['access','Tool access']);}
 if(!items.length){$('adm-content').innerHTML='<p style="color:var(--text3)">You have no admin access.</p>';return;}
 nav.innerHTML=items.map(([p,l])=>'<div class="nav-item" data-pane="'+p+'">'+l+'</div>').join('');
 nav.querySelectorAll('.nav-item').forEach(el=>el.onclick=()=>{const p=el.dataset.pane;show(p);
  if(p==='users')loadUsers();if(p==='groups'){loadUsers().then(loadGroups);}if(p==='tools')loadTools();if(p==='platform')loadPlatform();if(p==='usage')loadUsage();if(p==='logs')loadLogs();if(p==='telephony')loadTelephony();if(p==='agent'){loadAgent();loadWebex();}if(p==='rag')loadGroupRAG();if(p==='mcp')loadGroupMCP();if(p==='secrets')loadGroupSecrets();if(p==='access')loadAccess();});
 $('ag-group').onchange=()=>{loadAgent();loadWebex();}; if($('rg-group'))$('rg-group').onchange=loadGroupRAG; if($('mc-group'))$('mc-group').onchange=loadGroupMCP; if($('gs-group'))$('gs-group').onchange=loadGroupSecrets; $('ac-group').onchange=loadAccess;
 $('ag-model').innerHTML='<option value="">(server default)</option>'+MODELS.map(m=>'<option value="'+esc(m)+'">'+esc(m)+'</option>').join('');
 fillGroupPickers();
 show(items[0][0]);
 const first=items[0][0];
 if(first==='users')loadUsers();else if(first==='agent'){loadAgent();loadWebex();}
}
init();
</script></body></html>`
