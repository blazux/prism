const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../apps/notes.html'),'utf8');
function fn(name){const start=source.search(new RegExp('(?:async )?function '+name+'\\('));assert.ok(start>=0,name);return source.slice(start,source.indexOf('\n}',start)+2);}
function fixture(){
 const fields=Object.fromEntries(['e-body','e-title','e-tags','ar-status','ar-title','ar-text','ar-replace','ar-insert','ar-copy','ai-result'].map(id=>[id,{value:'',textContent:'',disabled:false,classList:{add(){},remove(){}}}]));
 fields['e-body'].value='Original text';Object.assign(fields['e-body'],{selectionStart:0,selectionEnd:8});
 const actions=[{disabled:false}],requests=[],saved=[],copied=[];
 const ctx=vm.createContext({$:id=>fields[id],document:{querySelectorAll:()=>actions},assist:()=>new Promise((resolve,reject)=>requests.push({resolve,reject})),saveNote:()=>saved.push(fields['e-body'].value),updatePreview(){},navigator:{clipboard:{writeText:text=>copied.push(text)}}});
 vm.runInContext("let selectedId='A',aiRequestVersion=0,aiEditVersion=0,aiResult=null,pendingNoteUpdate=null,mode='edit';function noteDraft(){return JSON.stringify(['e-title','e-tags','e-body'].map(id=>$(id).value))}"+['aiTarget','aiContextMatches','updateAIResultActions','runAI','applyResult','closeResult'].map(fn).join('\n'),ctx);
 return {fields,requests,saved,copied,actions,run:s=>vm.runInContext(s,ctx)};
}
test('AI progress and failures cannot replace or copy note content',async()=>{
 const f=fixture(),pending=f.run("runAI('doc_rewrite')");
 assert.equal(f.fields['ar-status'].textContent,'Working…');assert.equal(f.fields['ar-text'].textContent,'');
 for(const how of ['replace','insert','copy'])f.run(`applyResult('${how}')`);
 assert.equal(f.fields['e-body'].value,'Original text');assert.equal(f.saved.length,0);assert.equal(f.copied.length,0);assert.equal(f.fields['ar-replace'].disabled,true);
 f.requests[0].reject(Error('offline'));await pending;f.run("applyResult('replace')");
 assert.match(f.fields['ar-status'].textContent,/offline/);assert.equal(f.saved.length,0);assert.equal(f.actions[0].disabled,false);
});
test('successful AI output applies only to the unchanged source selection',async()=>{
 const f=fixture(),pending=f.run("runAI('doc_rewrite')");f.requests[0].resolve('Rewritten');await pending;
 assert.equal(f.fields['ar-replace'].disabled,false);f.run("applyResult('replace')");assert.equal(f.fields['e-body'].value,'Rewritten text');assert.deepEqual(f.saved,['Rewritten text']);
});
test('edits, changed selections, note switches and late older responses invalidate AI output',async()=>{
 for(const change of [f=>{f.fields['e-body'].value='New local edit';f.run('aiEditVersion++')},f=>{f.fields['e-body'].selectionStart=2},f=>f.run("selectedId='B'")]){
  const f=fixture(),pending=f.run("runAI('doc_rewrite')");change(f);f.requests[0].resolve('Stale output');await pending;f.run("applyResult('replace')");assert.equal(f.saved.length,0);assert.equal(f.fields['ar-replace'].disabled,true);
 }
 const f=fixture(),old=f.run("runAI('doc_rewrite')");f.run("closeResult();selectedId='B'");f.fields['e-body'].value='Another note';
 const newer=f.run("runAI('doc_rewrite')");f.requests[1].resolve('New result');await newer;f.requests[0].resolve('Old result');await old;
 assert.equal(f.fields['ar-text'].textContent,'New result');f.fields['e-body'].value='Changed after success';f.run('aiEditVersion++;updateAIResultActions()');assert.equal(f.fields['ar-replace'].disabled,true);f.run("applyResult('replace')");assert.equal(f.saved.length,0);
});
test('empty successful AI output is an error and dismiss cancels a pending result',async()=>{
 const f=fixture(),pending=f.run("runAI('doc_rewrite')");f.requests[0].resolve('');await pending;assert.equal(f.fields['ar-replace'].disabled,true);assert.match(f.fields['ar-status'].textContent,/No text/);
 const dismissed=f.run("runAI('doc_rewrite')");f.run('closeResult()');f.requests[1].resolve('Late');await dismissed;assert.equal(f.fields['ar-text'].textContent,'');f.run("applyResult('replace')");assert.equal(f.saved.length,0);
});
