// Outbound operations use the signed-in account, independently of the selected
// reception archive group. Credentials and ownership are resolved by Prism.
let callOffset = 0, callTotal = 0, callLoading = false, callSaving = false
for (const [id, key] of Object.entries({newCall:'newCall', phoneLabel:'phone', contactLabel:'contact', missionLabel:'mission', whenLabel:'when', nowLabel:'now', attemptsLabel:'attempts', scheduleCall:'schedule', myCalls:'myCalls', refreshOutbound:'refresh', refreshLogs:'refresh', previousCalls:'previousCalls', nextCalls:'nextCalls'})) $(id).textContent = t[key]
$('callTimezone').textContent = Intl.DateTimeFormat().resolvedOptions().timeZone
$('callNow').onchange = () => {
  $('callAt').disabled = $('callNow').checked
  $('callAt').required = !$('callNow').checked
}
$('refreshOutbound').onclick = () => outbound()
$('previousCalls').onclick = () => { callOffset = Math.max(0, callOffset - 25); outbound() }
$('nextCalls').onclick = () => { if (callOffset + 25 < callTotal) { callOffset += 25; outbound() } }

async function outbound() {
  if (callLoading) return
  callLoading = true
  try {
    const page = await api('calls', 'GET', null, {offset:callOffset, limit:25})
    callTotal = page.total
    if (callOffset >= callTotal && callOffset > 0) { callOffset = Math.max(0, Math.ceil(callTotal / 25) * 25 - 25); callLoading = false; return outbound() }
    $('outboundItems').replaceChildren()
    $('outboundTotal').textContent = String(callTotal)
    $('previousCalls').disabled = callOffset === 0
    $('nextCalls').disabled = callOffset + page.items.length >= callTotal
    if (!page.items.length) $('outboundItems').textContent = t.emptyQueue
    for (const call of page.items) {
      const row = document.createElement('article'); row.className = 'outbound-call'
      const heading = document.createElement('div'); heading.className = 'queue-heading'
      const who = document.createElement('strong'); who.textContent = call.contact_name ? call.contact_name + ' · ' + call.phone_number : call.phone_number
      const status = document.createElement('span'); status.className = 'badge'; status.textContent = t[call.status] || call.status
      heading.append(who, status)
      const mission = document.createElement('p'); mission.className = 'text'; mission.textContent = call.mission
      const when = document.createElement('p'); when.className = 'muted'; when.textContent = t.scheduled + ' · ' + new Date(call.scheduled_at).toLocaleString() + ' · ' + call.attempts + '/' + call.max_attempts
      row.append(heading, mission, when)
      if (call.result_summary) { const result = document.createElement('p'); result.className = 'text'; result.textContent = call.result_summary; row.append(result) }
      if (call.status === 'pending' || ['done','no_answer','failed','cancelled'].includes(call.status)) {
        const button = document.createElement('button'), cancel = call.status === 'pending'
        button.textContent = cancel ? t.cancel : t.retry
        button.onclick = async () => {
          button.disabled = true
          try { await api('calls/' + call.id + (cancel ? '' : '/retry'), cancel ? 'DELETE' : 'POST', cancel ? null : {}); error(); await outbound() }
          catch (e) { error(e.message); button.disabled = false }
        }
        row.append(button)
      }
      $('outboundItems').append(row)
    }
  } catch (e) { error(e.message) }
  finally { callLoading = false }
}

$('callForm').onsubmit = async event => {
  event.preventDefault()
  if (callSaving) return
  const at = $('callNow').checked ? null : new Date($('callAt').value)
  if (at && (!Number.isFinite(at.getTime()) || at.getTime() <= Date.now())) { error(t.invalidTime); return }
  callSaving = true; $('scheduleCall').disabled = true; $('callFeedback').textContent = ''
  try {
    await api('calls', 'POST', {phone_number:$('callPhone').value.trim(), contact_name:$('callContact').value.trim(), mission:$('callMission').value.trim(), scheduled_at:at ? at.toISOString() : null, max_attempts:Number($('callAttempts').value)})
    $('callForm').reset(); $('callNow').onchange(); callOffset = 0; error()
    $('callFeedback').textContent = t.scheduledOK
    await outbound()
  } catch (e) { error(e.message) }
  finally { callSaving = false; $('scheduleCall').disabled = false }
}
