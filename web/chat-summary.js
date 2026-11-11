// The panel is bound to the conversation captured when it opens. Late responses
// cannot populate another workspace; unsaved corrections stay with their chat.
window.PrismChatContext = (() => {
  let current = null
  let generation = 0
  const drafts = new Map()
  function close() {
    generation++
    current = null
    document.getElementById('chat-summary').hidden = true
    document.getElementById('chat-summary-button').setAttribute('aria-expanded', 'false')
  }
  async function request(url, options) {
    const response = await fetch(url, options)
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || 'Could not read or save the summary.')
    return data
  }
  async function toggle(session) {
    const pane = document.getElementById('chat-summary')
    if (current === session && !pane.hidden) { close(); return }
    current = session
    const requestGeneration = ++generation
    pane.hidden = false
    pane.textContent = 'Loading conversation summary…'
    document.getElementById('chat-summary-button').setAttribute('aria-expanded', 'true')
    const url = '/api/chat/context?session=' + encodeURIComponent(session)
    try {
      const data = await request(url)
      if (current !== session || generation !== requestGeneration) return
      pane.replaceChildren()
      const title = document.createElement('h3'); title.textContent = 'Conversation summary'; pane.append(title)
      const reload = document.createElement('button'); reload.className = 'btn'; reload.textContent = 'Reload summary'; pane.append(reload)
      reload.onclick = async () => {
        if ((drafts.has(session + ':context') || drafts.has(session + ':archive')) && !await PrismModal.confirm('Discard unsaved corrections and reload this conversation’s summary?')) return
        if (current !== session) return
        forget(session); toggle(session)
      }
      const note = document.createElement('p'); note.textContent = 'Summaries help the agent retain earlier exchanges. Corrections apply to this conversation after its current task finishes.'; pane.append(note)
      if (!data.summary && !data.archiveSummary) {
        const empty = document.createElement('p'); empty.textContent = 'No summary yet. Recent messages are used directly.'; pane.append(empty)
      }
      for (const [kind, field, label] of [['context', 'summary', 'Context summary'], ['archive', 'archiveSummary', 'Earlier history summary']]) {
        if (!data[field]) continue
        const key = session + ':' + kind
        const heading = document.createElement('label'); heading.textContent = label
        const text = document.createElement('textarea'); text.rows = 7; text.value = drafts.get(key)?.content ?? data[field]; heading.append(text); pane.append(heading)
        const status = document.createElement('p'); status.setAttribute('role', 'status')
        const save = document.createElement('button'); save.className = 'btn'; save.textContent = 'Save correction'; pane.append(save, status)
        let original = drafts.get(key)?.original ?? data[field]
        text.oninput = () => { drafts.set(key, {content: text.value, original}); status.textContent = 'Unsaved correction' }
        save.onclick = async () => {
          const content = text.value
          save.disabled = true; status.textContent = 'Saving…'
          try {
            await request(url, {method: 'PATCH', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({kind, content, original})})
            original = content
            if (text.value === content) { drafts.delete(key); status.textContent = 'Saved' }
            else { drafts.set(key, {content: text.value, original}); status.textContent = 'Saved · other changes remain unsaved' }
          } catch (e) { status.textContent = e.message }
          finally { save.disabled = false }
        }
      }
    } catch (e) { if (current === session && generation === requestGeneration) pane.textContent = e.message }
  }
  function forget(session) {
    drafts.delete(session + ':context'); drafts.delete(session + ':archive')
    if (current === session) close()
  }
  return {toggle, close, forget}
})()
