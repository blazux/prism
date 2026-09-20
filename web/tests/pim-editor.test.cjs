const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const ctx=vm.createContext({window:{},Date,Intl});vm.runInContext(fs.readFileSync(path.join(__dirname,'../pim-editor.js'),'utf8'),ctx);const pim=ctx.window.PrismPIM;
for(const zone of ['UTC','America/Martinique','Europe/Paris'])test('calendar duration, midnight and all-day ranges in '+zone,()=>{
 const previous=process.env.TZ;process.env.TZ=zone;
 try{
  const base={title:'Meeting',start:'2026-09-22T23:30',end:'2026-09-23T01:00',all_day:false,location:'Office',description:'Keep'};
  let n=pim.calendarPatch(base,{start:'2026-09-24T23:30'});assert.equal(n.end,'2026-09-25T01:00');assert.equal(n.location,'Office');assert.equal(n.description,'Keep');
  n=pim.calendarPatch({start:'2026-03-28',end:'2026-03-31',all_day:true},{start:'2026-10-24'});assert.equal(n.end,'2026-10-27');
  n=pim.calendarPatch(base,{all_day:true});assert.equal(n.start,'2026-09-22');assert.equal(n.end,'2026-09-24');
  n=pim.calendarPatch({start:'2026-09-22',end:'2026-09-23',all_day:true},{all_day:false});assert.equal(n.start,'2026-09-22T09:00');assert.equal(n.end,'2026-09-22T10:00');
  n=pim.calendarPatch(base,{start:'2026-09-24T15:00:00Z'});assert.equal(n.start,pim.local(new Date('2026-09-24T15:00:00Z')));
  assert.throws(()=>pim.calendarPatch(base,{end:'2026-09-22T22:00'}),/after start/);
  assert.equal(base.start,'2026-09-22T23:30');
 }finally{if(previous===undefined)delete process.env.TZ;else process.env.TZ=previous;}
});
test('task patches preserve omitted fields, clear due and validate before mutation',()=>{
 const base={title:'Original',priority:'normal',due:'2026-09-22T10:00'};
 const n=pim.taskPatch(base,{due:'',priority:'high'},false);assert.equal(n.due,'');assert.equal(n.title,'Original');
 assert.equal(pim.taskPatch(base,{due:'2026-09-25T14:30'},true).due,'2026-09-25');
 assert.throws(()=>pim.taskPatch(base,{priority:'invalid'},false),/Priority/);
 assert.throws(()=>pim.taskPatch(base,{due:'2026-02-30'},false),/Invalid/);
 assert.equal(base.priority,'normal');
});
// Exercise the actual adapters without a browser or network: applying a patch
// must update only form fields, never call persistence APIs.
function adapter(app){
 const html=fs.readFileSync(path.join(__dirname,'../apps/'+app+'.html'),'utf8');
 const fields=new Proxy({}, {get:(o,k)=>o[k]??=( {value:'',checked:false,textContent:'',classList:{contains:()=>k==='overlay'},style:{}} )});
 let options;
 const scope={PrismPIM:pim,PrismEditor:{connect:o=>(options=o,{snapshot:()=>options.read()})},$:id=>fields[id],editing:{id:'event-1'},calendarEditorGeneration:1,calendarSaving:false,tab:'todo',taskEditForm:null,taskCreateActive:true,taskEditorGeneration:1,taskAdding:false};
 scope.setFields=v=>{for(const [k,id] of Object.entries({title:'title',date:'date',enddate:'enddate',start:'start',end:'end',loc:'loc',desc:'desc'}))fields['f-'+id].value=v[k];fields['f-allday'].checked=v.allday;};
 const context=vm.createContext(scope),name=app==='tasks'?'taskEditor':'calendarEditor';const start=html.indexOf('const '+name+' = PrismEditor.connect(');const end=html.indexOf('\n});',start)+4;vm.runInContext(html.slice(start,end),context);
 return {fields,options,scope};
}
test('calendar adapter translates exclusive end back to inclusive UI without saving',()=>{
 const f=adapter('calendar');for(const [id,v] of Object.entries({'f-title':'Event','f-date':'2026-09-22','f-enddate':'2026-09-22','f-start':'09:00','f-end':'10:00'}))f.fields[id].value=v;
 f.options.update({start:'2026-09-25',all_day:true,end:'2026-09-28'});
 assert.equal(f.fields['f-enddate'].value,'2026-09-27');assert.equal(f.fields['f-allday'].checked,true);assert.equal(f.options.read().end,'2026-09-28');
});
test('new task adapter populates fields without saving',()=>{
 const f=adapter('tasks');f.fields['t-prio'].value='normal';f.options.update({title:'Prepare meeting',priority:'high',due:'2026-09-25'});
 assert.equal(f.fields['t-title'].value,'Prepare meeting');assert.equal(f.options.read().due,'2026-09-25');assert.equal(f.options.read().persistence,'form only; user must Add');
});
