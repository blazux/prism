const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const html=fs.readFileSync(path.join(__dirname,'../apps/email.html'),'utf8');
test('mail list sorts actual instants across offsets, with invalid dates last',()=>{
 const rows=[],opened=[],source=[
  {uid:1054,date:'2026-10-15T06:28:32Z'},
  {uid:1055,date:'2026-10-15T04:41:59-07:00'},
  {uid:4,date:'invalid'},
  {uid:5,date:'2026-10-15T13:41:59+02:00'},
  {uid:6,date:null},
 ];
 const ctx=vm.createContext({
  messages:source,current:null,mailTags:{},CAT_COLORS:{},Number,Date,
  renderFilters(){},updateMailHeading(){},passesFilter:()=>true,dateGroup:()=> 'Today',
  senderName:()=> 'Sender',avatarColor:()=>'',initial:()=>'',esc:s=>s||'',fmtDate:s=>s,
  openMessage:uid=>opened.push(uid),
  document:{getElementById:()=>({appendChild:row=>rows.push(row)}),createElement:()=>({setAttribute(){}})},
 });
 vm.runInContext(html.slice(html.indexOf('function renderList()'),html.indexOf('let readingGeneration=')),ctx);
 ctx.renderList();rows.filter(r=>r.className.startsWith('mrow')).forEach(r=>r.onclick());
 assert.deepEqual(opened,[1055,5,1054,6,4]);
 assert.deepEqual(source.map(m=>m.uid),[1054,1055,4,5,6]);
});
