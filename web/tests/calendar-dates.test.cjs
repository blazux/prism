// Date logic from the shipped inline app; no browser or synthetic DOM required.
// Run: node --test web/tests/calendar-dates.test.cjs
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const source = readFileSync(require('node:path').join(__dirname,'../apps/calendar.html'),'utf8');
const helpers = source.slice(source.indexOf('function pad('),source.indexOf('function setMode('));
const openForm = source.slice(source.indexOf('function openForm('),source.indexOf('function openEvent('));
for(const zone of ['UTC','America/Martinique','Europe/Paris']) {
 test('calendar dates in '+zone,()=>{
  const previous=process.env.TZ;process.env.TZ=zone;
  try {
   const context=vm.createContext({Date,String,calendarEditorGeneration:0,publishCalendarContext:()=>{},setTimeout:()=>{},$:()=>({style:{},classList:{add:()=>{}}})});
   vm.runInContext(helpers+openForm+'\nfunction setFields(v){saved=v}',context);
   assert.equal(vm.runInContext(`ymd(eventStart({allDay:true,startAt:'2026-09-22T00:00:00Z'}))`,context),'2026-09-22');
   const event=`{allDay:true,startAt:'2026-09-22T00:00:00Z',endAt:'2026-09-24T00:00:00Z'}`;
   assert.equal(vm.runInContext(`occursOn(${event},new Date('2026-09-23T12:00:00'))`,context),true);
   assert.equal(vm.runInContext(`occursOn(${event},new Date('2026-09-24T12:00:00'))`,context),false);
   assert.equal(vm.runInContext(`occursOn({allDay:false,startAt:'2026-09-22T23:30:00',endAt:'2026-09-23T01:00:00'},new Date('2026-09-23T12:00:00'))`,context),true);
   vm.runInContext(`openForm('2026-09-22','23:30',false)`,context);
   assert.equal(context.saved.enddate,'2026-09-23');
   assert.equal(context.saved.end,'00:30');
  } finally {if(previous===undefined)delete process.env.TZ;else process.env.TZ=previous;}
 });
}
