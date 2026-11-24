const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const html=fs.readFileSync(path.join(__dirname,'../apps/notes.html'),'utf8');
const wiki=fs.readFileSync(path.join(__dirname,'../apps/notes-wiki.js'),'utf8');
const rebase=wiki.slice(wiki.indexOf('function rebaseCanonicalNoteBody('),wiki.indexOf('\n}',wiki.indexOf('function rebaseCanonicalNoteBody('))+2);
function fn(name){const start=html.search(new RegExp('(?:async )?function '+name+'\\('));return html.slice(start,html.indexOf('\n}',start)+2);}
function fixture(api){
 const fields=Object.fromEntries(['e-title','e-body','e-tags','empty','editor','app','note-conflict'].map(id=>[id,{value:id==='e-body'?'unsaved':'',style:{},classList:{add(){},remove(){}}}]));
 const ctx=vm.createContext({$:id=>fields[id],api:async(path,options)=>{const result=await api(path,options);if(options){const patch=JSON.parse(options.body);vm.runInContext('notes',ctx).find(note=>note.id===patch.id)&&Object.assign(vm.runInContext('notes',ctx).find(note=>note.id===patch.id),patch);}return result;},session:'test',PrismAPI:{toast(){}},parent:{postMessage(){}},noteTags:()=>[],closeResult(){},setMode(){},applyNoteChrome(){},render(){},publishNoteContext(){},wikiRemember(){},wikiRenameID(){},updateWikiDetails(){},load:async()=>true});
 vm.runInContext(rebase,ctx);
 vm.runInContext("let notes=[{id:'A',scope:'personal',body:'old'},{id:'B',body:'other'}],selectedId='A',savedNoteDraft='',noteSaveQueue=Promise.resolve(true),pendingNoteUpdate=null,pendingCanonicalNote=null,aiEditVersion=0,mode='edit';function currentNote(){return notes.find(n=>n.id===selectedId)}function noteDraft(){return JSON.stringify(['e-title','e-body','e-tags'].map(id=>$(id).value))}"+['saveNote','persistNote','select','closeEditor'].map(fn).join('\n'),ctx);
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
test('a failed canonical reload never reapplies stale notes or resends un-reconciled frontmatter',async()=>{
 const before='---\ntitle: Old\n---\nOriginal',submitted='---\ntitle: Old\n---\nEdit B',canonical='---\ntitle: "New"\n---\nEdit B';
 const fields={'e-title':{value:'New'},'e-body':{value:submitted},'e-tags':{value:''}};
 let offline=true,server={id:'Old.md',title:'Old',body:before,tags:''};const writes=[];
 const ctx=vm.createContext({$:id=>fields[id],session:'test',PrismAPI:{toast(){}},setSourceBadge(){},render(){},updateWikiDetails(){},syncEditorFromServer(){},wikiRenameID(){},publishNoteContext(){},updatePreview(){},noteTags:()=>[],
  api:async(_path,options)=>{
   if(options){
    const patch=JSON.parse(options.body);writes.push(patch);
    server={...patch,id:'New.md',body:writes.length===1?canonical:patch.body};return {id:'New.md'};
   }
   if(offline)throw Error('503 unavailable');return {notes:[server],source:'vault'};
  }});
 vm.runInContext(rebase,ctx);
 ctx.before=before;ctx.submitted=submitted;
 vm.runInContext("let notes=[{id:'Old.md',title:'Old',body:before,tags:''}],selectedId='Old.md',savedNoteDraft=JSON.stringify(['Old',before,'']),noteSaveQueue=Promise.resolve(true),pendingNoteUpdate=null,pendingCanonicalNote=null,aiEditVersion=0,mode='edit',notesLoadVersion=0,notesSyncRequested=false;function currentNote(){return notes.find(n=>n.id===selectedId)}function noteDraft(){return JSON.stringify(['e-title','e-body','e-tags'].map(id=>$(id).value))}"+['load','saveNote','persistNote'].map(fn).join('\n'),ctx);
 const run=source=>vm.runInContext(source,ctx);
 assert.equal(await run('saveNote()'),false);
 assert.equal(writes.length,1);assert.equal(server.body,canonical);
 assert.equal(fields['e-body'].value,submitted);assert.equal(fields['e-title'].value,'New');
 assert.equal(await run('saveNote()'),false);assert.equal(writes.length,1);
 fields['e-body'].value+='\nTyped after failed refresh';offline=false;
 assert.equal(await run('saveNote()'),true);
 assert.equal(writes.length,2);assert.equal(writes[1].body,canonical+'\nTyped after failed refresh');
 assert.equal(fields['e-body'].value,writes[1].body);assert.equal(run('pendingCanonicalNote'),null);
});
