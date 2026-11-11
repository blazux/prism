// Shared request feedback and unsaved form state. No credentials are persisted.
window.PrismSettingsState = (() => {
  const fields = new Map()
  let initial = new WeakMap()
  const value = el => el.type === 'checkbox' ? el.checked : el.value
  const status = text => { const el=document.getElementById('settings-save-status');if(el)el.textContent=text }
  const dirty = () => {for(const [el] of fields)if(!el.isConnected)fields.delete(el);return [...fields].some(([el,original])=>value(el)!==original)}
  document.addEventListener('focusin', e=>{
    if(e.target.matches('#settings-content input:not([type=file]),#settings-content textarea,#settings-content select,#adm-content input:not([type=file]),#adm-content textarea,#adm-content select') && !e.target.id.endsWith('-group') && !(typeof currentTab!=='undefined'&&['tools','appearance'].includes(currentTab))&&!initial.has(e.target)) initial.set(e.target,value(e.target))
  })
  const changed = e=>{if(initial.has(e.target)){fields.set(e.target,initial.get(e.target));status(dirty()?'Unsaved changes':'')}}
  document.addEventListener('input',changed)
  document.addEventListener('change',changed)
  addEventListener('beforeunload',e=>{if(dirty()){e.preventDefault();e.returnValue=''}})
  function begin(controls = []) {
    status('Saving…')
    // The caller supplies the exact saved controls; equal values in another form
    // do not mean that form was sent. Capture before the request, not after it.
    if (!controls?.[Symbol.iterator]) controls = controls?.querySelectorAll?.('input,textarea,select') || []
    return Array.from(controls).filter(el => el?.isConnected && initial.has(el)).map(el => [el, value(el)])
  }
  function saved(snapshot) {
    for(const [el,sent] of snapshot){initial.set(el,sent);if(value(el)===sent)fields.delete(el);else fields.set(el,sent)}
    status(dirty()?'Saved · other changes remain unsaved':'Saved')
  }
  async function request(url,options={},controls=null) {
    const mutation=controls!==null && !['GET','HEAD'].includes((options.method||'GET').toUpperCase())
    const snapshot=mutation?begin(controls):[]
    try{const response=await fetch(url,options);if(mutation){if(response.ok)saved(snapshot);else status('Could not save · your edits are kept')}return response}
    catch(e){if(mutation)status('Could not save · your edits are kept');throw e}
  }
  return {dirty,begin,saved,status,request,clear:()=>{fields.clear();initial=new WeakMap()}}
})()
