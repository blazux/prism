let workSnapshot=null;
const workStates={
  waiting_for_input:['attention','Waiting for your input'],waiting_for_approval:['attention','Waiting for approval'],
  failed:['attention','Failed'],interrupted:['attention','Interrupted'],timed_out:['attention','Time limit reached'],
  running:['running','Working'],queued:['running','Queued'],completed:['finished','Finished'],stopped:['finished','Stopped']
};
function workDate(value){const date=typeof value==='string'?new Date(value):null;return date&&Number.isFinite(+date)?date:null;}
function workDuration(milliseconds){
  const seconds=Math.max(0,Math.floor(milliseconds/1000));
  if(seconds<60)return seconds+'s';
  if(seconds<3600)return Math.floor(seconds/60)+'m '+seconds%60+'s';
  return Math.floor(seconds/3600)+'h '+Math.floor(seconds%3600/60)+'m';
}
function workLink(value){
  if(typeof value!=='string')return null;
  try{
    const url=new URL(value,location.origin),view=PrismLinks.parse(url.href);
    if(url.origin!==location.origin||url.username||url.password||!view)return null;
    if(view.type!=='board'&&!(view.type==='app'&&view.name==='room'))return null;
    return url.pathname+url.search;
  }catch(_){return null;}
}
function groupAgentWork(items){
  const groups={attention:[],running:[],finished:[]};
  for(const item of items){const kind=workStates[item.runStatus]?.[0];if(kind)groups[kind].push(item);}
  for(const group of Object.values(groups))group.sort((a,b)=>{
    const time=item=>+(workDate(item.finishedAt)||workDate(item.lastActivityAt)||workDate(item.startedAt)||0);
    return time(b)-time(a)||String(a.name||'').localeCompare(String(b.name||''));
  });
  return groups;
}
function workElement(tag,className,text){
  const element=document.createElement(tag);if(className)element.className=className;if(text!==undefined)element.textContent=text;return element;
}
function renderWorkCard(item,observedAt,expanded){
  const kind=workStates[item.runStatus][0],card=workElement('article','work-card work-'+kind);
  card.dataset.workId=item.id;
  const head=workElement('div','work-card-head'),name=workElement('strong','work-name',item.name||'Conversation');
  const state=workElement('span','work-state',workStates[item.runStatus][1]);head.append(name,state);card.append(head);
  card.append(workElement('p','work-request',item.request||((item.backgroundJobs||0)>0?'Background agent work':'Conversation turn')));
  const meta=workElement('div','work-meta'),start=workDate(item.startedAt),last=workDate(item.lastActivityAt),end=workDate(item.finishedAt),observed=workDate(observedAt);
  if(start){
    const active=['running','queued','waiting_for_input','waiting_for_approval'].includes(item.runStatus);
    const text=end&&end>=start?'Duration '+workDuration(end-start):active&&observed&&observed>=start?'Elapsed '+workDuration(observed-start):'';
    if(text)meta.append(workElement('span','',text));
  }
  if(end){const time=workElement('time','','Ended '+end.toLocaleString());time.dateTime=end.toISOString();meta.append(time);}
  else if(last){const time=workElement('time','','Last event '+last.toLocaleString());time.dateTime=last.toISOString();meta.append(time);}
  else if(start){const time=workElement('time','','Started '+start.toLocaleString());time.dateTime=start.toISOString();meta.append(time);}
  if(item.queued>0)meta.append(workElement('span','',item.queued+' queued request(s)'));
  if(item.backgroundJobs>0)meta.append(workElement('span','',item.backgroundJobs+' background agent turn(s)'));
  if(meta.children.length)card.append(meta);
  if(item.runStatus==='waiting_for_input'||item.runStatus==='waiting_for_approval')card.append(workElement('p','work-guidance','Open the conversation to respond.'));
  if(['failed','interrupted','timed_out'].includes(item.runStatus))card.append(workElement('p','work-guidance','Review the conversation before continuing.'));
  const tools=item.recentTools||[],workers=item.subagents||[];
  if(tools.length||workers.length){
    const details=workElement('details','work-details');details.open=expanded.has(String(item.id));
    details.append(workElement('summary','','Tools & delegated work'));
    const list=workElement('ul','');
    for(const tool of tools)list.append(workElement('li','',(tool.tool||tool.name||'Tool')+' · '+String(tool.status||'').replaceAll('_',' ')));
    for(const worker of workers)list.append(workElement('li','','Worker: '+(worker.task||worker.id||'Delegated work')+' · '+String(worker.status||'').replaceAll('_',' ')));
    details.append(list);card.append(details);
  }
  const href=workLink(item.conversationUrl);
  if(href){const link=workElement('a','work-open','Open conversation');link.href=href;card.append(link);}
  return card;
}
function renderAgentWork(snapshot){
  const expanded=new Set([...feed.querySelectorAll('.work-card:has(details[open])')].map(card=>card.dataset.workId));
  const groups=groupAgentWork(snapshot.items),fragment=document.createDocumentFragment();
  const intro=workElement('div','work-intro');intro.append(workElement('p','','Latest tracked work in each conversation.'));
  const observed=workDate(snapshot.observedAt);if(observed){const time=workElement('time','','Observed '+observed.toLocaleTimeString());time.dateTime=observed.toISOString();intro.append(time);}fragment.append(intro);
  const sections=[['attention','Needs your attention','Nothing needs your attention.'],['running','In progress','No agent work in progress.'],['finished','Finished','No finished work to show.']];
  for(const [key,label,empty] of sections){
    const section=workElement('section','work-section');section.dataset.group=key;
    const heading=workElement('h2','',label),count=workElement('span','work-count',String(groups[key].length));heading.append(count);section.append(heading);
    const cards=workElement('div','work-grid');
    for(const item of groups[key])cards.append(renderWorkCard(item,snapshot.observedAt,expanded));
    if(!groups[key].length)cards.append(workElement('p','work-empty',empty));section.append(cards);fragment.append(section);
  }
  feed.replaceChildren(fragment);
}
async function loadAgentWork(){
  statusEl.className='work-status';
  if(!feed.querySelector('.work-intro')){
    if(workSnapshot)renderAgentWork(workSnapshot);
    else feed.replaceChildren();
  }
  if(!workSnapshot){statusEl.textContent='Loading agent work…';statusEl.style.display='block';}
  moreBtn.style.display='none';
  try{
    const snapshot=await PrismAPI.api('/api/activity/work');
    if(!snapshot||!Array.isArray(snapshot.items))throw Error('The server did not return an activity snapshot.');
    workSnapshot=snapshot;renderAgentWork(snapshot);statusEl.textContent='';statusEl.style.display='none';
  }catch(error){
    const observed=workDate(workSnapshot?.observedAt);
    statusEl.textContent='Could not refresh agent work. '+(workSnapshot?'Showing the last observation'+(observed?' from '+observed.toLocaleString():'')+'. ':'')+error.message;
    statusEl.style.display='block';
  }
}
