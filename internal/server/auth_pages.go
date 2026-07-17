package server

// Minimal self-contained auth pages (login / signup / admin approval). Styled
// inline so they need no external assets and render before the main app loads.
// The polished multi-user UI comes in Phase 6; these are functional now.

import "net/http"

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(loginPage))
}
func (s *Server) handleSignupPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(signupPage))
}

const authPageCSS = `
:root{color-scheme:light dark}
*{box-sizing:border-box}
body{font:15px/1.5 system-ui,sans-serif;max-width:420px;margin:4rem auto;padding:0 1.2rem}
h1{font-size:1.4rem;margin-bottom:.3rem}
.sub{color:#888;margin-bottom:1.6rem}
label{display:block;font-weight:600;margin-top:1rem}
input{width:100%;padding:.6rem;margin-top:.3rem;border:1px solid #8884;border-radius:8px;background:transparent;color:inherit;font:inherit}
button{margin-top:1.4rem;width:100%;padding:.65rem;border:0;border-radius:8px;background:#3b82f6;color:#fff;font:inherit;font-weight:600;cursor:pointer}
button:disabled{opacity:.5;cursor:default}
.msg{margin-top:1rem;padding:.7rem .9rem;border-radius:8px;font-weight:500;display:none}
.msg.err{display:block;background:#dc262622;color:#dc2626}
.msg.ok{display:block;background:#16a34a22;color:#16a34a}
.foot{margin-top:1.4rem;color:#888;font-size:.9em}
a{color:#3b82f6}
`

const loginPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Spectrum · Sign in</title>
<style>` + authPageCSS + `</style></head><body>
<h1>Sign in</h1><div class="sub">Spectrum workspace</div>
<label>Email<input id="email" type="email" autocomplete="username"></label>
<label>Password<input id="password" type="password" autocomplete="current-password"></label>
<button id="btn" onclick="login()">Sign in</button>
<div id="msg" class="msg"></div>
<div class="foot">No account? <a href="/signup">Create one</a></div>
<script>
const $=i=>document.getElementById(i);
function show(t,ok){const m=$('msg');m.textContent=t;m.className='msg '+(ok?'ok':'err');}
async function login(){
 $('btn').disabled=true;
 const r=await fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},
   body:JSON.stringify({email:$('email').value,password:$('password').value})});
 const d=await r.json().catch(()=>({}));
 if(r.ok){location.href='/';}else{show(d.error||'Sign in failed',false);$('btn').disabled=false;}
}
$('password').addEventListener('keydown',e=>{if(e.key==='Enter')login();});
</script></body></html>`

const signupPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Spectrum · Create account</title>
<style>` + authPageCSS + `</style></head><body>
<h1>Create account</h1><div class="sub">The first account becomes the admin. Others need admin approval.</div>
<label>Display name<input id="name" autocomplete="name"></label>
<label>Email<input id="email" type="email" autocomplete="username"></label>
<label>Password <span style="font-weight:400;color:#888">(min 8 characters)</span><input id="password" type="password" autocomplete="new-password"></label>
<button id="btn" onclick="signup()">Create account</button>
<div id="msg" class="msg"></div>
<div class="foot">Already have an account? <a href="/login">Sign in</a></div>
<script>
const $=i=>document.getElementById(i);
function show(t,ok){const m=$('msg');m.textContent=t;m.className='msg '+(ok?'ok':'err');}
async function signup(){
 $('btn').disabled=true;
 const r=await fetch('/api/signup',{method:'POST',headers:{'Content-Type':'application/json'},
   body:JSON.stringify({displayName:$('name').value,email:$('email').value,password:$('password').value})});
 const d=await r.json().catch(()=>({}));
 if(r.ok&&d.loggedIn){location.href='/';}
 else if(r.ok&&d.pending){show('Account created — waiting for an admin to approve it.',true);}
 else{show(d.error||'Signup failed',false);$('btn').disabled=false;}
}
</script></body></html>`
