const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const html=fs.readFileSync(path.join(__dirname,'../apps/notes.html'),'utf8');
function fn(name){const start=html.search(new RegExp('(?:async )?function '+name+'\\('));return html.slice(start,html.indexOf('\n}',start)+2);}
function fixture(api=async()=>({notes:[]})){
 const fields={'e-title':{value:'Title'},'e-body':{value:'Original'},'e-tags':{value:''},'note-conflict':{hidden:true}};
 const ctx=vm.createContext({$:id=>fields[id],api,session:'test',publishNoteContext(){},applyNoteChrome(){},updatePreview(){},closeEditor(){},render(){},setSourceBadge(){},PrismAPI:{toast(){}},noteTags:n=>(n.tags||'').split(',').map(t=>t.trim()).filter(Boolean)});
 vm.runInContext("let selectedId='n1',notes=[{id:'n1',title:'Title',body:'Original',tags:''}],mode='edit',notesLoadVersion=0,notesSyncRequested=false,pendingNoteUpdate=null,savedNoteDraft=JSON.stringify(['Title','Original','']),noteSaveQueue=Promise.resolve(true);function noteDraft(){return JSON.stringify(['e-title','e-body','e-tags'].map(id=>$(id).value))}function currentNote(){return notes.find(n=>n.id===selectedId)}"+['load','syncEditorFromServer','persistNote','saveNote','resolveNoteUpdate'].map(fn).join('\n'),ctx);
 return {fields,run:s=>vm.runInContext(s,ctx)};
}
test('agent updates replace a clean open note immediately',()=>{
 const f=fixture();f.run("notes[0].body='Agent revision';syncEditorFromServer()");assert.equal(f.fields['e-body'].value,'Agent revision');assert.equal(f.run('savedNoteDraft'),f.run('noteDraft()'));
});
test('remote changes preserve local typing and block autosave until resolved',async()=>{
 let writes=0;const f=fixture(async()=>{writes++;return {notes:[]}});f.fields['e-body'].value='My unsaved text';
 f.run("notes[0].body='Agent revision';syncEditorFromServer()");assert.equal(f.fields['e-body'].value,'My unsaved text');assert.equal(f.fields['note-conflict'].hidden,false);
 assert.equal(await f.run('saveNote()'),false);assert.equal(writes,0);
 await f.run('resolveNoteUpdate(false)');assert.equal(f.fields['e-body'].value,'Agent revision');assert.equal(f.fields['note-conflict'].hidden,true);
});
test('user can explicitly keep their edits after a remote update',async()=>{
 const writes=[];const f=fixture(async(_,opts)=>{if(opts)writes.push(JSON.parse(opts.body));return {notes:[{id:'n1',title:'Title',body:'My edit',tags:''}]}});
 f.fields['e-body'].value='My edit';f.run("notes[0].body='Agent revision';syncEditorFromServer()");await f.run('resolveNoteUpdate(true)');assert.equal(writes[0].body,'My edit');assert.equal(f.fields['note-conflict'].hidden,true);
});
test('a late fetch cannot undo newer data or drop a requested editor refresh',async()=>{
 const pending=[];const f=fixture(()=>new Promise(r=>pending.push(r)));
 const first=f.run('load(true)'),second=f.run('load(false)');pending[1]({notes:[{id:'n1',title:'Title',body:'Latest'}]});await second;pending[0]({notes:[{id:'n1',title:'Title',body:'Stale'}]});await first;
 assert.equal(f.fields['e-body'].value,'Latest');assert.equal(f.run('notes[0].body'),'Latest');
});
