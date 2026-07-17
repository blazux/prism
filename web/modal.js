// PrismModal — themed replacements for the native alert/confirm/prompt popups.
// Promise-based: await PrismModal.confirm('Delete?') → true/false;
// await PrismModal.prompt('Title', [{label,name,type,placeholder}]) → {name:value}|null;
//   (type:'textarea' renders a multi-line field)
// await PrismModal.alert('Message').
// Self-contained: injects its own styles, uses the page's CSS variables so it
// follows the active theme in the shell, settings, admin console and app iframes.
(function () {
  'use strict'

  const CSS = `
  .pm-overlay { position: fixed; inset: 0; z-index: 9999; display: flex; align-items: center;
    justify-content: center; background: rgba(0,0,0,.45); backdrop-filter: blur(2px);
    animation: pmFade .12s ease; }
  @keyframes pmFade { from { opacity: 0 } to { opacity: 1 } }
  .pm-box { background: var(--bg2, #16181f); color: var(--text, #e6e8ef);
    border: 1px solid var(--border2, var(--border, #2a2e3a)); border-radius: 12px;
    min-width: 320px; max-width: 440px; padding: 18px 20px 16px;
    box-shadow: 0 14px 48px rgba(0,0,0,.45); font: 13.5px/1.5 inherit;
    animation: pmPop .14s ease; }
  @keyframes pmPop { from { transform: scale(.96); opacity: 0 } to { transform: scale(1); opacity: 1 } }
  .pm-title { font-weight: 700; font-size: 14px; margin-bottom: 8px; }
  .pm-msg { color: var(--text2, var(--text, #cfd3de)); font-size: 13px; white-space: pre-wrap;
    word-break: break-word; margin-bottom: 4px; }
  .pm-field { margin: 10px 0 0; }
  .pm-field label { display: block; font-size: 11.5px; color: var(--text3, #8b90a0); margin-bottom: 4px; }
  .pm-field input { width: 100%; box-sizing: border-box; padding: 8px 10px; font: inherit;
    background: var(--bg, #0e1015); color: var(--text, #e6e8ef);
    border: 1px solid var(--border2, var(--border, #2a2e3a)); border-radius: 8px; }
  .pm-field input:focus { outline: none; border-color: var(--accent, #6b8afd); }
  .pm-foot { display: flex; gap: 8px; justify-content: flex-end; margin-top: 16px; }
  .pm-btn { padding: 7px 16px; border-radius: 8px; font: inherit; font-size: 13px; font-weight: 600;
    cursor: pointer; border: 1px solid var(--border2, var(--border, #2a2e3a));
    background: var(--bg, #0e1015); color: var(--text, #e6e8ef); }
  .pm-btn:hover { border-color: var(--accent, #6b8afd); }
  .pm-btn.pm-primary { background: var(--accent, #6b8afd); border-color: var(--accent, #6b8afd); color: #fff; }
  .pm-btn.pm-danger { background: var(--red, #e5534b); border-color: var(--red, #e5534b); color: #fff; }
  .pm-btn.pm-primary:hover, .pm-btn.pm-danger:hover { filter: brightness(1.1); }`

  function ensureStyle() {
    if (document.getElementById('pm-style')) return
    const st = document.createElement('style')
    st.id = 'pm-style'
    st.textContent = CSS
    document.head.appendChild(st)
  }

  // open builds the modal and resolves via the provided wire(resolve, box) hook.
  function open(build) {
    ensureStyle()
    return new Promise(resolve => {
      const ov = document.createElement('div')
      ov.className = 'pm-overlay'
      const box = document.createElement('div')
      box.className = 'pm-box'
      ov.appendChild(box)
      const done = v => { ov.remove(); document.removeEventListener('keydown', onKey, true); resolve(v) }
      const onKey = e => {
        if (e.key === 'Escape') { e.stopPropagation(); done(build.onEscape) }
        else if (e.key === 'Enter' && build.onEnter !== undefined && e.target.tagName !== 'BUTTON') {
          e.preventDefault(); e.stopPropagation(); build.onEnter(done)
        }
      }
      build.render(box, done)
      ov.addEventListener('mousedown', e => { if (e.target === ov) done(build.onEscape) })
      document.addEventListener('keydown', onKey, true)
      document.body.appendChild(ov)
      const f = box.querySelector('input, button.pm-primary, button.pm-danger, button')
      if (f) f.focus()
    })
  }

  function btn(label, cls, onclick) {
    const b = document.createElement('button')
    b.className = 'pm-btn' + (cls ? ' ' + cls : '')
    b.textContent = label
    b.onclick = onclick
    return b
  }

  const esc = s => String(s == null ? '' : s)

  const PrismModal = {
    alert(message, opts) {
      const o = opts || {}
      return open({
        onEscape: undefined,
        onEnter: done => done(undefined),
        render(box, done) {
          if (o.title) { const t = document.createElement('div'); t.className = 'pm-title'; t.textContent = o.title; box.appendChild(t) }
          const m = document.createElement('div'); m.className = 'pm-msg'; m.textContent = esc(message); box.appendChild(m)
          const foot = document.createElement('div'); foot.className = 'pm-foot'
          foot.appendChild(btn(o.okLabel || 'OK', 'pm-primary', () => done(undefined)))
          box.appendChild(foot)
        },
      })
    },

    confirm(message, opts) {
      const o = opts || {}
      return open({
        onEscape: false,
        onEnter: done => done(true),
        render(box, done) {
          const t = document.createElement('div'); t.className = 'pm-title'; t.textContent = o.title || 'Confirm'; box.appendChild(t)
          const m = document.createElement('div'); m.className = 'pm-msg'; m.textContent = esc(message); box.appendChild(m)
          const foot = document.createElement('div'); foot.className = 'pm-foot'
          foot.appendChild(btn(o.cancelLabel || 'Cancel', '', () => done(false)))
          foot.appendChild(btn(o.okLabel || (o.danger ? 'Delete' : 'OK'), o.danger ? 'pm-danger' : 'pm-primary', () => done(true)))
          box.appendChild(foot)
        },
      })
    },

    // fields: [{name, label, type?, placeholder?, value?}] → resolves {name: value} or null.
    prompt(title, fields, opts) {
      const o = opts || {}
      const fs = (fields && fields.length) ? fields : [{ name: 'value', label: '' }]
      return open({
        onEscape: null,
        onEnter: done => submit(done),
        render(box, done) {
          const t = document.createElement('div'); t.className = 'pm-title'; t.textContent = esc(title); box.appendChild(t)
          if (o.message) { const m = document.createElement('div'); m.className = 'pm-msg'; m.textContent = o.message; box.appendChild(m) }
          for (const f of fs) {
            const w = document.createElement('div'); w.className = 'pm-field'
            if (f.label) { const l = document.createElement('label'); l.textContent = f.label; w.appendChild(l) }
            // type:'textarea' for multi-line fields (e.g. a collection description);
            // everything else is an <input> of that type.
            const inp = document.createElement(f.type === 'textarea' ? 'textarea' : 'input')
            if (f.type !== 'textarea') inp.type = f.type || 'text'
            else { inp.rows = f.rows || 3; inp.style.resize = 'vertical' }
            inp.placeholder = f.placeholder || ''
            inp.value = f.value || ''
            inp.dataset.pmName = f.name
            inp.autocomplete = 'off'
            w.appendChild(inp)
            box.appendChild(w)
          }
          const foot = document.createElement('div'); foot.className = 'pm-foot'
          foot.appendChild(btn(o.cancelLabel || 'Cancel', '', () => done(null)))
          foot.appendChild(btn(o.okLabel || 'Save', 'pm-primary', () => submit(done)))
          box.appendChild(foot)
        },
      })
      function submit(done) {
        const out = {}
        let ok = true
        document.querySelectorAll('.pm-box input[data-pm-name], .pm-box textarea[data-pm-name]').forEach(inp => {
          out[inp.dataset.pmName] = inp.value
          if (!inp.value.trim()) { ok = false; inp.style.borderColor = 'var(--red, #e5534b)' }
        })
        if (ok) done(out)
      }
    },
  }

  window.PrismModal = PrismModal
})()
