async function renderMemoryPanel(container) {
  const pane=document.createElement('div');pane.style.cssText='padding:16px;overflow:auto'
  pane.innerHTML=settingsHeading('Memory','Your profile and learned facts are shared across your personal workspaces.','Personal')
  container.append(pane)
  const url='/api/memory'
  try {
    const response=await settingsFetch(url);if(!response.ok)throw new Error('Could not load memory')
    const data=await response.json()
    if(!data.knowledgeAvailable){const msg=document.createElement('p');msg.textContent='Saved facts are unavailable while document search is disconnected or being configured.';pane.append(msg);return}
    for(const [kind,label] of [['profile','Profile facts'],['learnings','Learned facts']]) {
      const heading=document.createElement('h3');heading.textContent=label;pane.append(heading)
      if(!data[kind]?.length){const empty=document.createElement('p');empty.textContent='Nothing saved yet.';pane.append(empty)}
      for(const doc of data[kind]||[]) {
        const card=document.createElement('details');card.className='memory-card'
        const summary=document.createElement('summary');summary.textContent=doc.filename;card.append(summary);pane.append(card)
        let loaded=false
        card.addEventListener('toggle',async()=>{
          if(!card.open||loaded)return;loaded=true
          try {
            const res=await settingsFetch(url+'?kind='+kind+'&name='+encodeURIComponent(doc.filename));if(!res.ok)throw new Error('Could not read saved text')
            const item=await res.json();const text=document.createElement('textarea');text.rows=7;text.value=item.content;text.readOnly=!!item.truncated;card.append(text)
            if(item.truncated){const note=document.createElement('p');note.textContent='This memory is too long to edit here. You can still delete it.';card.append(note)}
            for(const [method,label] of [['PATCH','Save correction'],['DELETE','Delete']]) {
              const button=document.createElement('button');button.className='btn';button.textContent=label;button.disabled=method==='PATCH'&&item.truncated;card.append(button)
              button.onclick=async()=>{
                if(method==='DELETE'&&!await PrismModal.confirm('Delete this saved memory?',{danger:true}))return
                button.disabled=true
                try{await settingsFetch(url,{method,headers:{'Content-Type':'application/json'},body:JSON.stringify({kind,name:doc.filename,content:text.value,revision:item.revision})},method==='PATCH'?[text]:[]);if(method==='DELETE')card.remove();else{const fresh=await settingsFetch(url+'?kind='+kind+'&name='+encodeURIComponent(doc.filename)).then(r=>r.json());item.revision=fresh.revision}}
                catch(e){showToast(e.message,true)}finally{button.disabled=false}
              }
            }
          }catch(e){loaded=false;showToast(e.message,true)}
        })
      }
    }
  } catch(e){const error=document.createElement('p');error.textContent=e.message;pane.append(error)}
}
