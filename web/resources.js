// Shared maintenance UI for settings and dashboard deletion. All decisions are
// rechecked server-side; a preview never authorizes stale or shared deletions.
;(function () {
  async function request(session, args, query = {}) {
    const params = new URLSearchParams({ session, ...query })
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 35000)
    try {
      const response = await fetch(`/api/resources?${params}`, args ? {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(args), signal: controller.signal,
      } : { signal: controller.signal })
      const raw = await response.text()
      let data
      try { data = JSON.parse(raw) } catch { data = null }
      if (!response.ok) throw new Error(data?.error || raw || `Request failed (${response.status})`)
      return data
    } finally { clearTimeout(timeout) }
  }

  function summary(result) {
    const removed = result.removed || []
    const kept = result.plan?.keep || []
    return `${removed.length} resource(s) removed.${kept.length ? ` ${kept.length} retained; review them in Settings → Resources.` : ''}`
  }

  function confirm(title, result, history = false, maintenance = false) {
    return PrismModal.open({
      onEscape: null,
      render(box, done) {
        box.style.maxWidth = '540px'
        const heading = document.createElement('div')
        heading.className = 'pm-title'; heading.textContent = title
        const intro = document.createElement('div')
        intro.className = 'pm-msg'
        intro.textContent = maintenance ? 'Review the selected resources. Shared resources will be retained.' : history ? 'The workspace, its widgets and conversation history will be deleted.' : 'The widget and its dedicated, unused backends will be deleted.'
        const list = document.createElement('div')
        list.style.cssText = 'max-height:300px;overflow:auto;margin-top:10px;font-size:12px'
        const selected = new Set()
        const remove = result.plan?.remove || []
        const keep = result.plan?.keep || []
        for (const node of remove) {
          const row = document.createElement('div')
          row.style.cssText = 'padding:4px 0;color:var(--red)'
          row.textContent = `Delete ${node.id}`
          list.appendChild(row)
        }
        for (const node of keep) {
          const row = document.createElement('label')
          row.style.cssText = 'display:flex;align-items:flex-start;gap:8px;padding:5px 0;color:var(--text2)'
          const checkbox = document.createElement('input')
          checkbox.type = 'checkbox'; checkbox.disabled = !!node.protected
          const toggle = document.createElement('span'); toggle.className = 'toggle-switch'
          const track = document.createElement('span'); track.className = 'toggle-track'
          toggle.append(checkbox, track)
          checkbox.onchange = () => checkbox.checked ? selected.add(node.id) : selected.delete(node.id)
          const text = document.createElement('span')
          text.textContent = `${node.id} — ${node.reason || 'Retained by default.'}`
          row.append(toggle, text); list.appendChild(row)
        }
        const warning = document.createElement('div')
        warning.className = 'pm-msg'; warning.style.marginTop = '10px'
        warning.textContent = keep.length ? 'Data and legacy resources are kept unless selected. Shared resources stay protected. Deletion cannot be undone.' : 'Deletion cannot be undone.'
        const foot = document.createElement('div'); foot.className = 'pm-foot'
        foot.append(PrismModal.btn('Cancel', '', () => done(null)), PrismModal.btn('Delete', 'pm-danger', () => done([...selected])))
        box.append(heading, intro, list, warning, foot)
      },
    })
  }
  window.PrismResources = { request, summary, confirm }
})()
