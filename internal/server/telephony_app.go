package server

import (
	"net/http"
	"strings"
)

// The personal Settings page embeds this editor. Multi-user deployments use
// the same form in Admin. App access never grants configuration privileges.
func (s *Server) handleTelephonySettingsPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || (s.cfg.MultiUser && !u.IsGlobalAdmin()) {
		writeErr(w, http.StatusForbidden, "global admin only")
		return
	}
	if s.cfg.VoxURL == "" {
		writeErr(w, http.StatusServiceUnavailable, "PrismVox is disconnected")
		return
	}
	if s.cfg.MultiUser {
		http.Redirect(w, r, "/admin#telephony", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	pane := strings.Replace(adminTelephonyPane, `class="pane"`, `class="pane active"`, 1)
	_, _ = w.Write([]byte(telephonySettingsHead + pane + telephonySettingsScript + adminTelephonyJS + "\nloadTelephony().catch(e=>{$('page-error').textContent=e.message;});</script></body></html>"))
}

func (s *Server) handleLegacyTelephonySettingsPage(w http.ResponseWriter, r *http.Request) {
	target := "/settings.html#telephony"
	if s.cfg.MultiUser {
		target = "/admin#telephony"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleLegacyTelephonyPage(w http.ResponseWriter, r *http.Request) {
	target := "/apps/telephony.html"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

const telephonySettingsHead = `<!doctype html><html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Telephony settings — Prism</title><link rel="stylesheet" href="/style.css">
<link rel="stylesheet" href="/admin-console.css"><script src="/theme.js"></script><script src="/modal.js"></script>
</head><body><div id="adm"><main id="adm-main"><div id="adm-content"><p id="page-error" role="alert"></p>`

const telephonySettingsScript = `</div></main></div><script>
const $=id=>document.getElementById(id);
window.addEventListener('message',e=>{
  if(e.source!==parent||e.data?.type!=='prism-theme'||!e.data.vars)return;
  for(const [key,value] of Object.entries(e.data.vars))document.documentElement.style.setProperty(key,value);
});
window.addEventListener('storage',e=>{if(e.key===PrismTheme.KEYS.FONT)PrismTheme.applyFont();});
function esc(s){return String(s??'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));}
async function jget(url){const r=await fetch(url);return r.ok?r.json():null;}
async function jpost(url,body){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});}
function showPicked(input,label){const f=$(input).files[0];$(label).textContent=f?f.name:'';}
`
