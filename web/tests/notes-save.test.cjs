const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const html=fs.readFileSync(path.join(__dirname,'../apps/notes.html'),'utf8');
function fn(name){const start=html.search(new RegExp('(?:async )?function '+name+'\\('));return html.slice(start,html.indexOf('\n}',start)+2);}
function fixture(api){
 const fields=Object.fromEntries(['e-title','e-body','e-tags','empty','editor','app','note-conflict'].map(id=>[id,{value:id==='e-body'?'unsaved':'',style:{},classList:{add(){},remove(){}}}]));
 const ctx=vm.createContext({$:id=>fields[id],api,session:'test',PrismAPI:{toast(){}},parent:{postMessage(){}},noteTags:()=>[],closeResult(){},setMode(){},applyNoteChrome(){},render(){},publishNoteContext(){},load:async()=>{}});
 vm.runInContext("let notes=[{id:'A',scope:'personal',body:'old'},{id:'B',body:'other'}],selectedId='A',savedNoteDraft='',noteSaveQueue=Promise.resolve(true),pendingNoteUpdate=null;function currentNote(){return notes.find(n=>n.id===selectedId)}function noteDraft(){return JSON.stringify(['e-title','e-body','e-tags'].map(id=>$(id).value))}"+['saveNote','persistNote','select','closeEditor'].map(fn).join('\n'),ctx);
 return {ctx,fields,run:s=>vm.runInContext(s,ctx)};
}
test('failed save blocks navigation and close, keeps input, and can be retried',async()=>{
 let offline=true;const f=fixture(async()=>{if(offline)throw Error('offline')});
 await f.run("select('B')");assert.equal(f.run('selectedId'),'A');assert.equal(f.fields['e-body'].value,'unsaved');
 await f.run('closeEditor()');assert.notEqual(f.fields.editor.style.display,'none');
 offline=false;await f.run("select('B')");assert.equal(f.run('selectedId'),'B');assert.equal(f.fields['e-body'].value,'other');
});
test('typing during a pending save is persisted before navigation; blur saves serialize',async()=>{
 const writes=[];let finish;
 const f=fixture(async(_,opts)=>{writes.push(JSON.parse(opts.body));if(writes.length===1)await new Promise(r=>finish=r)});
 const first=f.run('saveNote()');await Promise.resolve();
 f.fields['e-body'].value='latest';const nav=f.run("select('B')");finish();await Promise.all([first,nav]);
 assert.deepEqual(writes.map(x=>[x.id,x.body]),[['A','unsaved'],['A','latest']]);assert.equal(f.run('selectedId'),'B');
});
test('clicking the selected note preserves edits; a saved closed note can reopen',async()=>{
 const f=fixture(async()=>{});await f.run("select('A')");assert.equal(f.fields['e-body'].value,'unsaved');
 await f.run('closeEditor()');assert.equal(f.run('selectedId'),null);await f.run("select('A')");assert.equal(f.run('selectedId'),'A');assert.equal(f.fields.editor.style.display,'flex');
});
