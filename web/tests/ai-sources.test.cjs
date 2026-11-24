const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {readFileSync}=require('node:fs');
const path=require('node:path');
const code=readFileSync(path.join(__dirname,'../ai-settings.js'),'utf8');
function fixture(){
 const values={sourceName:'Secondary',provider:'other',baseURL:'http://secondary/v1',apiKey:'typed-key',clearKey:false,model:'same-model',chatVision:true,embeddingMode:'default',embeddingSource:'primary', 'embed-provider':'ollama','embed-baseURL':'http://embedding:11434','embed-apiKey':'','embed-clearKey':false,'embed-model':'embed','reindex':false};
 const fields=Object.fromEntries(Object.entries(values).map(([k,v])=>[k,typeof v==='boolean'?{checked:v}:{value:v}]));
 const ctx=vm.createContext({f:k=>fields[k]});
 const start=code.indexOf('  function syncSource()'),end=code.indexOf('  function showModels(');
 const source=code.match(/^  const source = .*$/m)?.[0],catalogKey=code.match(/^  const catalogKey = .*$/m)?.[0];
 assert.ok(start>=0&&end>start&&source&&catalogKey,'extract the current staged payload helpers');
 vm.runInContext(`let selectedID='secondary',defaultID='primary',sources=[{id:'primary',name:'Primary',provider:'openai',baseURL:'https://api.openai.com/v1',model:'primary-model',apiKey:''},{id:'secondary',name:'Secondary',provider:'other',baseURL:'http://secondary/v1',model:'old-model',apiKey:''}];const embeddingKey=Symbol('embedding'),catalogs=new Map();`+source+'\n'+catalogKey+'\n'+code.slice(start,end),ctx);
 const run=expr=>JSON.parse(JSON.stringify(vm.runInContext(expr,ctx)));
 return {fields,ctx,run};
}
test('editing a secondary source retains default and other connections on save',()=>{
 const f=fixture(),p=f.run("payload('save')");
 assert.equal(p.defaultSource,'primary');assert.equal(p.model,'primary-model');assert.equal(p.sources.length,2);
 assert.equal(p.sources[1].apiKey,'typed-key');assert.equal(p.sources[0].apiKey,'');assert.equal(p.sources[1].model,'same-model');
});
test('testing a source sends only that connection without changing the chosen default',()=>{
 const f=fixture(),p=f.run("payload('test')");
 assert.equal(p.sourceID,'secondary');assert.equal(p.defaultSource,'secondary');assert.equal(p.sources.length,1);
 assert.equal(f.run('defaultID'),'primary');assert.equal(f.run('sources.length'),2);
});
test('same-provider embeddings follow the default rather than the source being edited',()=>{
 const f=fixture();assert.equal(f.run('effective()').baseURL,'https://api.openai.com/v1');
 vm.runInContext("defaultID='secondary'",f.ctx);
 assert.equal(f.run('effective()').baseURL,'http://secondary/v1');
});
test('embedding source reference and dedicated connection remain independent',()=>{
 const f=fixture();f.fields.embeddingMode.value='source';f.fields.embeddingSource.value='secondary';
 assert.equal(f.run('effective()').baseURL,'http://secondary/v1');
 assert.equal(f.run("payload('save')").embedding.sourceID,'secondary');
 f.fields.embeddingMode.value='dedicated';assert.equal(f.run('effective()').baseURL,'http://embedding:11434');
});
test('disabled document search preserves dedicated credentials unless removal is explicitly staged',()=>{
 const f=fixture();f.fields.embeddingMode.value='disabled';f.fields['embed-apiKey'].value='replacement';
 let p=f.run("payload('save')");assert.equal(p.embedding.model,'');assert.equal(p.embedding.apiKey,'replacement');assert.equal(p.embedding.clearKey,false);
 f.fields['embed-apiKey'].value='';f.fields['embed-clearKey'].checked=true;
 p=f.run("payload('save')");assert.equal(p.embedding.clearKey,true);assert.equal(p.embedding.apiKey,'');assert.equal(p.sources[1].clearKey,false);
});
test('model catalogs stay isolated per connection and from embeddings',()=>{
 const f=fixture();f.run("catalogs.set(catalogKey(''),['secondary-model']);catalogs.set(catalogKey('embed-'),['embedding-model']);selectedID='primary';catalogs.set(catalogKey(''),['primary-model']);0");
 assert.deepEqual(f.run("catalogs.get(catalogKey(''))"),['primary-model']);assert.deepEqual(f.run("catalogs.get(catalogKey('embed-'))"),['embedding-model']);
 f.run("selectedID='secondary';0");assert.deepEqual(f.run("catalogs.get(catalogKey(''))"),['secondary-model']);
});
