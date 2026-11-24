const {test}=require('node:test');
const assert=require('node:assert/strict');
const {readFileSync}=require('node:fs');
const path=require('node:path'),vm=require('node:vm');
const wiki=readFileSync(path.join(__dirname,'../apps/notes-wiki.js'),'utf8');
const marked=readFileSync(path.join(__dirname,'../vendor/marked/marked.min.js'),'utf8');
function fixture(){
 const fields={'wiki-back':{},'wiki-forward':{}};
 const ctx=vm.createContext({window:{},$:id=>fields[id],PrismAPI:{toast(){}},PrismLinks:{parse:href=>{
   const url=new URL(href,'https://prism.test');return url.origin==='https://prism.test'&&url.pathname==='/'&&url.searchParams.get('app')==='notes'?{type:'app',name:'notes',id:url.searchParams.get('id')}:null;
 }}});
 vm.runInContext(marked,ctx);ctx.window.marked=ctx.marked;
 vm.runInContext("let notes=[],selectedId=null;function currentNote(){return notes.find(n=>n.id===selectedId)}",ctx);
 vm.runInContext(wiki,ctx);
 return {ctx,fields,run:s=>vm.runInContext(s,ctx),json:s=>JSON.parse(JSON.stringify(vm.runInContext(s,ctx)))};
}
test('title links stay in their source scope and duplicate titles remain ambiguous',()=>{
 const f=fixture();f.run("notes=[{id:'1',title:'Config',scope:'personal'},{id:'2',title:'Config',scope:'personal'},{id:'3',title:'Config',scope:'group'}]");
 assert.deepEqual(f.json("wikiCandidates('Config','personal').map(n=>n.id)"),['1','2']);
 assert.deepEqual(f.json("wikiCandidates('Config','group').map(n=>n.id)"),['3']);
 assert.deepEqual(f.json("wikiCandidates('1','group').map(n=>n.id)"),['1']);
 f.run("notes=notes.filter(n=>n.scope!=='group')");
 assert.deepEqual(f.json("wikiCandidates('Config','group')"),[]);
});
test('aliases preserve former titles and vault paths after a rename',()=>{
 const f=fixture();f.run("notes=[{id:'projects/New name.md',title:'New name',scope:'personal',aliases:['Old name','projects/Old name.md','projects/New name']}]");
 for(const target of ['Old name','projects/Old name.md','projects/Old name','projects/New name'])assert.equal(f.json(`wikiCandidates(${JSON.stringify(target)})`)[0].id,'projects/New name.md');
});
test('wiki link indexing ignores inline, fenced and indented code and keeps list links',()=>{
 const f=fixture();const body='[[Real|Display label]]\n\n`[[Inline code]]`\n\n```md\n[[Fenced code]]\n```\n\n    [[Indented code]]\n\n- [[List page]]\n- [Stable page](/?app=notes&id=42)\n\n![image](https://example.test/image.png)';
 assert.deepEqual(f.json(`wikiTargets(${JSON.stringify(body)})`),['Real','List page','42']);
});
test('failed page navigation keeps the current history position',async()=>{
 const f=fixture();f.run("notes=[{id:'A'},{id:'B'}];wikiRemember('A');wikiRemember('B');");
 f.ctx.select=async()=>false;await f.run('navigateWiki(-1)');assert.equal(f.run('wikiNavigationIndex'),1);
 f.ctx.select=async()=>true;await f.run('navigateWiki(-1)');assert.equal(f.run('wikiNavigationIndex'),0);
 f.run("wikiRenameID('A','renamed.md')");assert.deepEqual(f.json('wikiNavigation'),['renamed.md','B']);
});
test('canonical frontmatter rename rebases only an unchanged title line over newer typing',()=>{
 const f=fixture(),before='---\ntitle: Old\ntags: [ops]\n---\nText',after='---\ntitle: "New"\ntags: [ops]\n---\nText';
 f.ctx.before=before;f.ctx.after=after;f.ctx.local=before+'\nTyped while saving';
 assert.equal(f.run('rebaseCanonicalNoteBody(before,after,local)'),after+'\nTyped while saving');
 f.ctx.local=before.replace('title: Old','title: User choice')+'\nTyped while saving';
 assert.equal(f.run('rebaseCanonicalNoteBody(before,after,local)'),f.ctx.local);
 assert.equal(f.run('rebaseCanonicalNoteBody(before,after,before)'),after);
});
